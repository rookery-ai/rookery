// Package fonts holds the single copy of the UI font.
//
// It is its own package because go:embed cannot reach outside its own
// directory, and two independent consumers need these exact bytes:
//
//   - the Go export path (internal/export inlines it as a data: URI so an
//     exported HTML/PDF is self-contained and needs no font installed on the
//     server — ToPDF shells out to a headless renderer that would otherwise
//     silently substitute a system font while appearing to succeed), and
//   - the SPA (web/ui imports it through the "@fonts" Vite alias, which
//     fingerprints it into dist/ and therefore into the embedded binary).
//
// A second checked-in copy would drift silently, so there is deliberately
// only one. The SPA must never fetch this from a CDN: Rookery ships as a
// single binary for offline/LAN installs, where an external font request
// fails outright.
package fonts

import _ "embed"

// InterVariableWOFF2 is Inter Variable, latin subset, weights 100-900.
//
//go:embed InterVariable.woff2
var InterVariableWOFF2 []byte
