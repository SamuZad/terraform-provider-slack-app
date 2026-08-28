package main

// Generate registry documentation into docs/ from the provider schema and the
// examples/ tree. Run via `go generate ./...` or `make generate`.
//go:generate go tool tfplugindocs generate --provider-name slack-app
