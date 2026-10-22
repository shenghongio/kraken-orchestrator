package banner

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/shenghongio/kraken-orchestrator/internal/version"
)

//go:embed ascii-art.txt
var asciiArt string

func Print(name string) {
	info := version.Get()
	art := strings.TrimRight(asciiArt, "\n")

	fmt.Fprintln(os.Stdout, art)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "  %s\n", name)
	fmt.Fprintf(os.Stdout, "  Version:    %s\n", info.Version)
	fmt.Fprintf(os.Stdout, "  Commit:     %s\n", info.Commit)
	fmt.Fprintf(os.Stdout, "  Build Time: %s\n", info.BuildTime)
	fmt.Fprintf(os.Stdout, "  Go Version: %s\n", info.GoVersion)
	fmt.Fprintf(os.Stdout, "  OS/Arch:    %s/%s\n", info.GoOS, info.GoArch)
	fmt.Fprintln(os.Stdout)
}
