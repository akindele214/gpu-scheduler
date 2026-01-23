package config

import (
	"fmt"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

func Load() (*Config, error) {
	godotenv.Load()
	viper.SetEnvPrefix("GPU_SCHEDULER")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	viper.SetDefault("scheduler.port", 8888)
	viper.SetDefault("scheduler.metricsPort", 9090)
	viper.SetDefault("queue.defaultPolicy", "fifo")
	viper.SetDefault("gpu.pollIntervalSeconds", 5)
	viper.SetDefault("kubernetes.namespace", "default")
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")
	// Set up viper to read the config.yaml file
	viper.SetConfigName("config") // Config file name without extension
	viper.SetConfigType("yaml")   // Config file type
	viper.AddConfigPath(".")      // Look for the config file in the current directory

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode into struct: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("error validating config: %w", err)
	}

	return &cfg, nil
}
