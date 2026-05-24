module github.com/xtrinode/xtrinode/tools

go 1.26.3

tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint

tool sigs.k8s.io/controller-tools/cmd/controller-gen

require (
	github.com/golangci/golangci-lint/v2 v2.12.2
	sigs.k8s.io/controller-tools v0.21.0
)
