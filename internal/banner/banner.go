package banner

import (
	"fmt"
	"os"
	
	"github.com/mbndr/figlet4go"
	"github.com/shenghongio/kraken-orchestrator/internal/config"
)

func Print(name string, cfg *config.SchedulerConfig) {
	renderer := figlet4go.NewAsciiRender()
	art, _ := renderer.Render(name)
	
	fmt.Fprint(os.Stdout, art)
	fmt.Fprint(os.Stdout, "\n", "scheduler service port:\t", "["+cfg.HTTP.Addr+"]", "\n")
	fmt.Fprintln(os.Stdout)
}
