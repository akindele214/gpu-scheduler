package config

import "fmt"

type Config struct {
	Scheduler  SchedulerConfig  `mapstructure:"scheduler" json:"scheduler"`
	Queue      QueueConfig      `mapstructure:"queue" json:"queue"`
	Workflows  WorkflowConfig   `mapstructure:"workflows" json:"workflows"`
	GPU        GPUConfig        `mapstructure:"gpu" json:"gpu"`
	Kubernetes KubernetesConfig `mapstructure:"kubernetes" json:"kubernetes"`
	Logging    LoggingConfig    `mapstructure:"logging" json:"logging"`
}

type SchedulerConfig struct {
	Name                     string `mapstructure:"name" json:"name"`
	Port                     int    `mapstructure:"port" json:"port"`
	MetricsPort              int    `mapstructure:"metricsPort" json:"metrics_port"`
	Mode                     string `mapstructure:"mode" json:"mode"`
	GangTimeoutSeconds       int    `mapstructure:"gangTimeoutSeconds" json:"gang_timeout_seconds"`
	PreemptionEnabled        bool   `mapstructure:"preemptionEnabled" json:"preemption_enabled"`
	CheckpointTimeoutSeconds int    `mapstructure:"checkpointTimeoutSeconds" json:"checkpoint_timeout_seconds"`
	PreemptionGracePeriod    int    `mapstructure:"preemptionGracePeriod" json:"preemption_grace_period"`
	AutoResumeMaxRetries     int    `mapstructure:"autoResumeMaxRetries" json:"auto_resume_max_retries"`
	AutoResumePriorityBoost  int    `mapstructure:"autoResumePriorityBoost" json:"auto_resume_priority_boost"`
}

type WorkFlowTypeConfig struct {
	Name        string `mapstructure:"name" json:"name"`
	Priority    int    `mapstructure:"priority" json:"priority"`
	Preemptible bool   `mapstructure:"preemptible" json:"preemptible"`
}

type WorkflowConfig struct {
	Enabled         bool                 `mapstructure:"enabled" json:"enabled"`
	AllowCustom     bool                 `mapstructure:"allowCustom" json:"allow_custom"`
	DefaultPriority int                  `mapstructure:"defaultPriority" json:"default_priority"`
	Types           []WorkFlowTypeConfig `mapstructure:"types" json:"types"`
}

type QueueConfig struct {
	MaxSize       int    `mapstructure:"maxSize" json:"max_size"`
	DefaultPolicy string `mapstructure:"defaultPolicy" json:"default_policy"`
}

type GPUConfig struct {
	PollIntervalSeconds int  `mapstructure:"pollIntervalSeconds" json:"poll_interval_seconds"`
	MockMode            bool `mapstructure:"mockMode" json:"mock_mode"`
}

type KubernetesConfig struct {
	Kubeconfig string `mapstructure:"kubeconfig" json:"kubeconfig"`
	Namespace  string `mapstructure:"namespace" json:"namespace"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level" json:"level"`
	Format string `mapstructure:"format" json:"format"`
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
