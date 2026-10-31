package config

import (
	"reflect"
	"testing"
	"time"

	"routenest/internal/domain"
)

func fullProxy() *Proxy {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &Proxy{
		Strategy:   "round_robin",
		Headers:    map[string]string{"X-A": "1"},
		ModelChain: []string{"m1", "m2"},
		OnlyFree:   true,
		Combos: []domain.ModelCombo{{
			ID: "c1", Name: "C", Enabled: true, CreatedAt: now,
			Entries: []domain.ComboEntry{{Model: "m1", Source: "zen"}},
		}},
		MaxRequestBodyBytes:       1024,
		NonstreamKeepaliveSeconds: 5,
		ModelAliases:              []ModelAlias{{Alias: "a", Target: "b"}},
		ExcludedModels:            []string{"x"},
		RequestErrorRules:         []RequestErrorRule{{StatusCodes: []int{429}, Match: "limit", Action: "continue"}},
	}
}

// Clone must copy every field and share no reference with the original. The
// check walks the struct with reflection, so a new reference field that Clone
// forgets is caught here.
func TestCloneIsDeepAndComplete(t *testing.T) {
	src := fullProxy()
	dst := src.Clone()
	if !reflect.DeepEqual(src, dst) {
		t.Fatalf("clone differs from source:\n%+v\n%+v", src, dst)
	}
	assertNoSharedRefs(t, reflect.ValueOf(src).Elem(), reflect.ValueOf(dst).Elem(), "Proxy")

	dst.Headers["X-A"] = "changed"
	dst.ModelChain[0] = "changed"
	dst.Combos[0].Entries[0].Model = "changed"
	dst.ModelAliases[0].Alias = "changed"
	dst.RequestErrorRules[0].StatusCodes[0] = 500
	if src.Headers["X-A"] != "1" || src.ModelChain[0] != "m1" || src.Combos[0].Entries[0].Model != "m1" ||
		src.ModelAliases[0].Alias != "a" || src.RequestErrorRules[0].StatusCodes[0] != 429 {
		t.Fatal("mutating the clone changed the source")
	}
}

func TestCloneKeepsNilAndEmpty(t *testing.T) {
	src := &Proxy{ModelChain: []string{}, ExcludedModels: nil}
	dst := src.Clone()
	if dst.ModelChain == nil || len(dst.ModelChain) != 0 {
		t.Errorf("empty slice not kept: %#v", dst.ModelChain)
	}
	if dst.ExcludedModels != nil || dst.Headers != nil || dst.Combos != nil {
		t.Errorf("nil not kept: %#v", dst)
	}
	var none *Proxy
	if none.Clone() != nil {
		t.Error("nil receiver must clone to nil")
	}
}

// assertNoSharedRefs fails when a slice, map or pointer field of dst aliases the
// same storage as the corresponding field of src. Nested values are checked too.
func assertNoSharedRefs(t *testing.T, src, dst reflect.Value, path string) {
	t.Helper()
	switch src.Kind() {
	case reflect.Struct:
		for i := 0; i < src.NumField(); i++ {
			assertNoSharedRefs(t, src.Field(i), dst.Field(i), path+"."+src.Type().Field(i).Name)
		}
	case reflect.Slice:
		if src.Len() > 0 && src.Pointer() == dst.Pointer() {
			t.Errorf("%s shares its backing array", path)
		}
		for i := 0; i < src.Len() && i < dst.Len(); i++ {
			assertNoSharedRefs(t, src.Index(i), dst.Index(i), path+"[]")
		}
	case reflect.Map:
		if !src.IsNil() && src.Pointer() == dst.Pointer() {
			t.Errorf("%s shares its map", path)
		}
	case reflect.Ptr:
		if !src.IsNil() && src.Pointer() == dst.Pointer() {
			t.Errorf("%s shares its pointer", path)
		}
	}
}
