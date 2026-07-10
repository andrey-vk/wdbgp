// Package version carries the build-time version string.
package version

// Version is overwritten at build time via
//
//	-ldflags "-X github.com/andrey-vk/wdbgp/internal/version.Version=..."
//
// Release images get the semver matching their Docker tag (from the
// release tag via deploy.yml); runtime/build.sh stamps `git describe`
// output. "dev" means a build that bypassed both.
var Version = "dev"
