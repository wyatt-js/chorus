// This nested module exists only to make the parent module's Go tooling
// (`go build ./...`, `go mod tidy`) ignore the vendored submodules under
// third_party/, some of which contain unrelated Go source. Native deps are
// consumed by sidecar processes, not Go imports.
module github.com/wyattjs/chorus/third_party

go 1.25
