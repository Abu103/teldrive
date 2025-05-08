package version

import (
	"runtime"

	"github.com/Abu103/teldrive/internal/api"
)

var (
	Version   = "development"
	CommitSHA = "unknown"
)

func GetVersionInfo() *api.ApiVersion {
	return &api.ApiVersion{
		Version:   Version,
		CommitSHA: CommitSHA,
		GoVersion: runtime.Version(),
		Os:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}
