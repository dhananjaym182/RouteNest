package adminauth

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	auth "routenest/internal/adminauth"
)

const inspectionAttempts = 5
const inspectionRetryDelay = 10 * time.Millisecond
const inspectionSnapshotLimit = 16 << 20
const inspectionJournalLimit = 512 << 20

var ErrMissing = errors.New("administrator authority is missing or incomplete")
var ErrUncertain = errors.New("administrator authority changed during inspection; retry status")
var ErrPrepared = errors.New("administrator journal contains a prepared transaction; restart RouteNest to complete it")

type inspectionPath struct {
	path      string
	directory bool
	info      os.FileInfo
}

type inspectionRoot interface {
	Close() error
	OpenFile(string, int, os.FileMode) (*os.File, error)
	Stat(string) (os.FileInfo, error)
}

type inspectionIdentity interface {
	inspectionIdentity() (uint64, uint64, bool)
}

func Inspect(ctx context.Context, dataDir string) (auth.State, error) {
	if dataDir == "" {
		return auth.State{}, fmt.Errorf("administrator data directory is required")
	}
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return auth.State{}, err
	}
	return retryInspection(ctx, func() (auth.State, error) {
		return inspectOnce(ctx, dir)
	})
}

func retryInspection(ctx context.Context, read func() (auth.State, error)) (auth.State, error) {
	var err error
	for attempt := 0; attempt < inspectionAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return auth.State{}, err
		}
		var state auth.State
		state, err = read()
		if err == nil {
			return state, nil
		}
		if !errors.Is(err, ErrUncertain) && !errors.Is(err, ErrPrepared) && !errors.Is(err, ErrMissing) && !errors.Is(err, ErrCorrupt) {
			return auth.State{}, err
		}
		if attempt+1 < inspectionAttempts {
			timer := time.NewTimer(inspectionRetryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return auth.State{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return auth.State{}, err
}

func inspectOnce(ctx context.Context, dataDir string) (auth.State, error) {
	dir := filepath.Join(dataDir, "admin-auth")
	paths := []inspectionPath{
		{path: dataDir, directory: true},
		{path: dir, directory: true},
		{path: filepath.Join(dir, SnapshotName)},
		{path: filepath.Join(dir, JournalName)},
		{path: filepath.Join(dir, LockName)},
	}
	for i := range paths {
		info, err := inspectSecureInfo(paths[i].path, paths[i].directory)
		if err != nil {
			return auth.State{}, err
		}
		paths[i].info = info
		if paths[i].directory && info == nil {
			return auth.State{}, ErrMissing
		}
	}
	root, err := openInspectionRoot(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return auth.State{}, ErrUncertain
		}
		return auth.State{}, err
	}
	defer root.Close()
	held, err := root.Stat(".")
	if err != nil {
		return auth.State{}, err
	}
	if !sameInspectionInfo(paths[1].info, held, true) {
		return auth.State{}, ErrUncertain
	}
	state, readErr := inspectFiles(ctx, root, paths[2].info, paths[3].info, paths[4].info)
	if err := ctx.Err(); err != nil {
		return auth.State{}, err
	}
	for _, path := range paths {
		info, err := inspectSecureInfo(path.path, path.directory)
		if err != nil {
			return auth.State{}, err
		}
		if !sameInspectionInfo(path.info, info, path.directory) {
			return auth.State{}, ErrUncertain
		}
	}
	return state, readErr
}

func inspectSecureInfo(path string, directory bool) (os.FileInfo, error) {
	info, err := inspectionPathInfo(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := checkSecurePath(path, directory); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrUncertain
		}
		return nil, err
	}
	return info, nil
}

func sameInspectionInfo(before, after os.FileInfo, directory bool) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return sameInspectionFile(before, after) && before.Mode() == after.Mode() && (directory || before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()))
}

func sameInspectionFile(before, after os.FileInfo) bool {
	left, leftOK := before.(inspectionIdentity)
	right, rightOK := after.(inspectionIdentity)
	if leftOK || rightOK {
		if !leftOK || !rightOK {
			return false
		}
		leftVolume, leftFile, leftValid := left.inspectionIdentity()
		rightVolume, rightFile, rightValid := right.inspectionIdentity()
		return leftValid && rightValid && leftVolume == rightVolume && leftFile == rightFile
	}
	return os.SameFile(before, after)
}

func openInspectionFile(root inspectionRoot, name string, expected os.FileInfo) (*os.File, error) {
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrUncertain
		}
		return nil, err
	}
	info, err := inspectionFileInfo(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || !sameInspectionInfo(expected, info, false) {
		file.Close()
		return nil, ErrUncertain
	}
	return file, nil
}

func inspectFiles(ctx context.Context, root inspectionRoot, snapshotInfo, journalInfo, lockInfo os.FileInfo) (auth.State, error) {
	if journalInfo == nil || lockInfo == nil {
		return auth.State{}, ErrMissing
	}
	var disk snapshot
	if snapshotInfo != nil {
		var err error
		disk, err = inspectSnapshot(ctx, root, snapshotInfo)
		if err != nil {
			return auth.State{}, err
		}
	}
	committed, pending, err := inspectJournal(ctx, root, journalInfo)
	if err != nil {
		return auth.State{}, err
	}
	if pending != nil {
		if snapshotInfo == nil && committed.Revision != 0 {
			return auth.State{}, ErrMissing
		}
		if snapshotInfo != nil && !(disk.Revision == committed.Revision && disk.StateHash == stateHash(committed)) && !(disk.Revision == pending.NextRevision && disk.StateHash == pending.StateHash) {
			return auth.State{}, fmt.Errorf("%w: snapshot differs from prepared state", ErrCorrupt)
		}
		return auth.State{}, ErrPrepared
	}
	if snapshotInfo == nil {
		return auth.State{}, ErrMissing
	}
	if disk.Revision != committed.Revision || disk.StateHash != stateHash(committed) {
		return auth.State{}, fmt.Errorf("%w: snapshot differs from committed journal", ErrCorrupt)
	}
	return disk.State, nil
}

func inspectSnapshot(ctx context.Context, root inspectionRoot, info os.FileInfo) (snapshot, error) {
	if info.Size() > inspectionSnapshotLimit {
		return snapshot{}, fmt.Errorf("%w: snapshot size limit", ErrCorrupt)
	}
	file, err := openInspectionFile(root, SnapshotName, info)
	if err != nil {
		return snapshot{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(inspectionReader{ctx, io.LimitReader(file, info.Size())})
	if err != nil {
		return snapshot{}, err
	}
	var value snapshot
	if strictJSON(data, &value) != nil {
		return snapshot{}, fmt.Errorf("%w: snapshot encoding", ErrCorrupt)
	}
	if value.Revision != value.State.Revision || value.StateHash != stateHash(value.State) {
		return snapshot{}, fmt.Errorf("%w: snapshot hash", ErrCorrupt)
	}
	if auth.ValidateState(value.State) != nil {
		return snapshot{}, fmt.Errorf("%w: snapshot state", ErrCorrupt)
	}
	return value, nil
}

func inspectJournal(ctx context.Context, root inspectionRoot, info os.FileInfo) (auth.State, *record, error) {
	if info.Size() > inspectionJournalLimit {
		return auth.State{}, nil, fmt.Errorf("%w: journal size limit", ErrCorrupt)
	}
	file, err := openInspectionFile(root, JournalName, info)
	if err != nil {
		return auth.State{}, nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(inspectionReader{ctx, io.LimitReader(file, info.Size())})
	scanner.Buffer(make([]byte, 64<<10), inspectionSnapshotLimit+1)
	scanner.Split(splitInspectionRecord)
	committed := auth.EmptyState()
	var pending *record
	var sequence uint64
	head := ""
	seen := map[string]bool{}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return auth.State{}, nil, err
		}
		line := scanner.Bytes()
		if len(line) > inspectionSnapshotLimit {
			return auth.State{}, nil, fmt.Errorf("%w: journal record size limit", ErrCorrupt)
		}
		var next record
		if strictJSON(line, &next) != nil {
			return auth.State{}, nil, fmt.Errorf("%w: journal encoding", ErrCorrupt)
		}
		if next.Sequence != sequence+1 || next.PreviousHash != head || next.Hash != next.digest() {
			return auth.State{}, nil, fmt.Errorf("%w: journal chain", ErrCorrupt)
		}
		switch next.Type {
		case "prepare":
			if pending != nil || next.PreviousRevision != committed.Revision || next.NextRevision != committed.Revision+1 || next.State == nil || next.Event == nil || next.PrepareHash != "" || next.State.Revision != next.NextRevision || next.StateHash != stateHash(*next.State) || auth.ValidateState(*next.State) != nil || validateEvent(*next.Event) != nil || seen[next.Event.ID] {
				return auth.State{}, nil, fmt.Errorf("%w: prepare record", ErrCorrupt)
			}
			if committed.Initialized && !next.State.Initialized {
				return auth.State{}, nil, fmt.Errorf("%w: initialized downgrade", ErrCorrupt)
			}
			seen[next.Event.ID] = true
			pending = &next
		case "commit":
			if pending == nil || next.State != nil || next.Event != nil || next.PrepareHash != pending.Hash || next.PreviousRevision != pending.PreviousRevision || next.NextRevision != pending.NextRevision || next.StateHash != pending.StateHash {
				return auth.State{}, nil, fmt.Errorf("%w: commit record", ErrCorrupt)
			}
			committed = *pending.State
			pending = nil
		default:
			return auth.State{}, nil, fmt.Errorf("%w: unknown journal record", ErrCorrupt)
		}
		sequence = next.Sequence
		head = next.Hash
	}
	if err := ctx.Err(); err != nil {
		return auth.State{}, nil, err
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return auth.State{}, nil, fmt.Errorf("%w: journal record size limit", ErrCorrupt)
		}
		return auth.State{}, nil, err
	}
	return committed, pending, nil
}

func splitInspectionRecord(data []byte, atEOF bool) (int, []byte, error) {
	if end := bytes.IndexByte(data, '\n'); end >= 0 {
		return end + 1, data[:end+1], nil
	}
	if atEOF && len(data) != 0 {
		return 0, nil, fmt.Errorf("%w: truncated journal record", ErrCorrupt)
	}
	return 0, nil, nil
}

type inspectionReader struct {
	ctx context.Context
	io.Reader
}

func (reader inspectionReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.Reader.Read(data)
}
