module github.com/gabrielvillalongasimon/vmrunner/agent

go 1.26

toolchain go1.26.0

require (
	capnproto.org/go/capnp/v3 v3.1.0-alpha.2
	github.com/gabrielvillalongasimon/vmrunner/api v0.0.0
	github.com/vishvananda/netlink v1.3.1
)

require (
	github.com/colega/zeropool v0.0.0-20230505084239-6fb4a4f75381 // indirect
	golang.org/x/sync v0.7.0 // indirect
)

replace github.com/gabrielvillalongasimon/vmrunner/api => ../api
