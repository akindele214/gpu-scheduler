package config

import "fmt"

type Config struct {
	Scheduler  SchedulerConfig  `mapstructure:"scheduler"`
	Queue      QueueConfig      `mapstructure:"queue"`
	GPU        GPUConfig        `mapstructure:"gpu"`
	Kubernetes KubernetesConfig `mapstructure:"kubernetes"`
	Logging    LoggingConfig    `mapstructure:"logging"`
}

type SchedulerConfig struct {
	Name        string `mapstructure:"name"`
	Port        int    `mapstructure:"port"`
	MetricsPort int    `mapstructure:"metricsPort"`
	Mode        string `mapstructure:mode`
}

type QueueConfig struct {
	MaxSize       int    `mapstructure:"maxSize"`
	DefaultPolicy string `mapstructure:"defaultPolicy"`
}

type GPUConfig struct {
	PollIntervalSeconds int  `mapstructure:"pollIntervalSeconds"`
	MockMode            bool `mapstructure:"mockMode"`
}

type KubernetesConfig struct {
	Kubeconfig string `mapstructure:"kubeconfig"`
	Namespace  string `mapstructure:"namespace"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

func (config *Config) Validate() error {
	if config.Scheduler.Name == "" {
		return fmt.Errorf("scheduler.name is required")
	}
	if config.Queue.MaxSize <= 0 {
		return fmt.Errorf("queue.maxSize must be greater than 0")
	}
	if config.Scheduler.Port < 1 || config.Scheduler.Port > 655535 {
		return fmt.Errorf("scheduler.port should be between 1-65535")
	}
	if config.Scheduler.MetricsPort < 1 || config.Scheduler.MetricsPort > 655535 {
		return fmt.Errorf("scheduler.metricsPort should be between 1-65535")
	}
	valid := map[string]bool{
		"fifo":    true,
		"binpack": true,
	}
	if !valid[config.Queue.DefaultPolicy] {
		return fmt.Errorf("queue.defaultPolicy must be 'fifo' or 'binpack'")
	}

	// Logging validation
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[config.Logging.Level] {
		return fmt.Errorf("logging.level must be 'debug', 'info', 'warn', or 'error'")
	}

	validFormats := map[string]bool{"json": true, "text": true}
	if !validFormats[config.Logging.Format] {
		return fmt.Errorf("logging.format must be 'json' or 'text'")
	}
	return nil
}
