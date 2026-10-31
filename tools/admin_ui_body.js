// Reads the admin page body as the concatenation of its HTML fragments, in the
// order declared by adminUIBodyParts in internal/admin/ui_manifest.go.
// Used by the i18n gate tools so they see the same markup the server serves.
const fs = require('fs');
const path = require('path');

function adminBody(root) {
  const manifest = fs.readFileSync(path.join(root, 'internal/admin/ui_manifest.go'), 'utf8');
  const m = manifest.match(/adminUIBodyParts = \[\]string\{([\s\S]*?)\n\}/);
  if (!m) throw new Error('adminUIBodyParts not found in ui_manifest.go');
  const names = [...m[1].matchAll(/"([^"]+)"/g)].map(x => x[1]);
  return names.map(n => fs.readFileSync(path.join(root, 'internal/admin', n), 'utf8')).join('');
}

module.exports = { adminBody };
