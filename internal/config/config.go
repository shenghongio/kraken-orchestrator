package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type LogConfig struct {
	Level  string `koanf:"level"`
	Format string `koanf:"format"`
}

type HTTPConfig struct {
	Addr         string        `koanf:"addr"`
	ReadTimeout  time.Duration `koanf:"read_timeout"`
	WriteTimeout time.Duration `koanf:"write_timeout"`
}

type DBConfig struct {
	DSN             string        `koanf:"dsn"`
	MaxOpenConns    int           `koanf:"max_open_conns"`
	MaxIdleConns    int           `koanf:"max_idle_conns"`
	ConnMaxLifetime time.Duration `koanf:"conn_max_lifetime"`
}
type RedisConfig struct {
	Addr     string `koanf:"addr"`
	Password string `koanf:"password"`
	DB       int    `koanf:"db"`
}

func Load(configFile string, delimiters ...string) (*koanf.Koanf, error) {
	k := koanf.New(".")

	// 默认值由调用放通过koanf.Set 设置，或直接在struct 赋零值后覆盖
	// YAML 文件
	if configFile != "" {
		if err := k.Load(file.Provider(configFile), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("load config from file: %w", err)
		}
	}
	// 环境变量覆盖，前缀 KRAKEN_，双下划线 __ 表示层级分隔
	// KRAKEN_DB__DSN -> db.dsn
	// KRAKEN_HTTP__READ_TIMEOUT -> http.read_timeout
	if err := k.Load(env.Provider(".", env.Opt{
		Prefix: "KRAKEN_",
		TransformFunc: func(k, v string) (string, any) {
			k = strings.TrimPrefix(k, "KRAKEN_")
			k = strings.ReplaceAll(k, "__", ".")
			return strings.ToLower(k), v
		},
	}), nil); err != nil {
		return nil, fmt.Errorf("load config from env: %w", err)
	}
	return k, nil
}
