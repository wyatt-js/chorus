// This nested module exists only to make the parent module's Go tooling
// (`go build ./...`, `go mod tidy`) ignore the vendored C/Swift submodules under
// third_party/, some of which contain unrelated Go source. The native deps are
// consumed by internal/raop via cgo filesystem paths, not Go imports.
module github.com/wyattjs/airtooth-sync/third_party

go 1.25
