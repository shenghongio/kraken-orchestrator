package main

import (
	"flag"
	"os"
	
	"github.com/shenghongio/kraken-orchestrator/internal/config"
)

const defaultConfig = "./configs/scheduler.yaml"

// 解析配置文件路径 flag > env > default
func loadConfig() (*config.SchedulerConfig, string, error) {
	configFile := flag.String("config", defaultConfig, "path to kraken-orchestrator config file")
	flag.Parse()
	if !isFlagPassed("config") {
		if envPath := os.Getenv("KRAKEN_SCHEDULER_CONFIG_PATH"); envPath != "" {
			*configFile = envPath
		}
	}
	cfg, err := config.LoadSchedulerConfig(*configFile)
	return cfg, *configFile, err
}

// isFlagPasswd 判断指定flag 是否被用户显示设置
func isFlagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
