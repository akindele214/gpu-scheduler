package config

import "fmt"

type Config struct {
	Scheduler  SchedulerConfig  `mapstructure:"scheduler"`
	Queue      QueueConfig      `mapstructure:"queue"`
	Workflows  WorkflowConfig   `mapstructure:"workflows"`
	GPU        GPUConfig        `mapstructure:"gpu"`
	Kubernetes KubernetesConfig `mapstructure:"kubernetes"`
	Logging    LoggingConfig    `mapstructure:"logging"`
}

type SchedulerConfig struct {
	Name                     string `mapstructure:"name"`
	Port                     int    `mapstructure:"port"`
	MetricsPort              int    `mapstructure:"metricsPort"`
	Mode                     string `mapstructure:"mode"`
	GangTimeoutSeconds       int    `mapstructure:"gangTimeoutSeconds"`
	PreemptionEnabled        bool   `mapstructure:"preemptionEnabled"`
	CheckpointTimeoutSeconds int    `mapstructure:"checkpointTimeoutSeconds"`
	PreemptionGracePeriod    int    `mapstructure:"preemptionGracePeriod"`
}

type WorkFlowTypeConfig struct {
	Name        string `mapstructure:"name"`
	Priority    int    `mapstructure:"priority"`
	Preemptible bool   `mapstructure:"preemptible"`
}

type WorkflowConfig struct {
	Enabled         bool                 `mapstructure:"enabled"`
	AllowCustom     bool                 `mapstructure:"allowCustom"`
	DefaultPriority int                  `mapstructure:"defaultPriority"`
	Types           []WorkFlowTypeConfig `mapstructure:"types"`
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
	if config.Workflows.Enabled {
		if config.Workflows.DefaultPriority < 0 || config.Workflows.DefaultPriority > 100 {
			return fmt.Errorf("workflows.defaultPriority must be between 0-100")
		}
		if len(config.Workflows.Types) < 1 {
			return fmt.Errorf("workflows.types must have at least one workflow when enabled")
		}
		for i, wf := range config.Workflows.Types {
			if wf.Name == "" {
				return fmt.Errorf("workflow at index [%d] name cannot be empty", i)
			}
			if wf.Priority < 0 || wf.Priority > 100 {
				return fmt.Errorf("workflow '%s' priority must be between 0-100", wf.Name)
			}
		}
	}
	if config.Scheduler.MetricsPort < 1 || config.Scheduler.MetricsPort > 655535 {
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
