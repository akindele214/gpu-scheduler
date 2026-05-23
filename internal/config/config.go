package config

import (
	"fmt"
	"strings"
)

type Config struct {
	Scheduler   SchedulerConfig   `mapstructure:"scheduler" json:"scheduler"`
	Queue       QueueConfig       `mapstructure:"queue" json:"queue"`
	Workflows   WorkflowConfig    `mapstructure:"workflows" json:"workflows"`
	GPU         GPUConfig         `mapstructure:"gpu" json:"gpu"`
	Kubernetes  KubernetesConfig  `mapstructure:"kubernetes" json:"kubernetes"`
	Logging     LoggingConfig     `mapstructure:"logging" json:"logging"`
	ProxyConfig ProxyConfig       `mapstructure:"proxy" json:"proxy"`
	Rebalancing RebalancingConfig `mapstructure:"rebalancing" json:"rebalancing"`
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

type ProxyConfig struct {
	Enabled      bool   `mapstructure:"enabled" json:"enabled"`
	Port         int    `mapstructure:"port" json:"port"`
	SchedulerURL string `mapstructure:"scheduler_url" json:"scheduler_url"`
}

type RebalancingConfig struct {
	Enabled              bool          `mapstructure:"enabled" json:"enabled"`
	DryRun               bool          `mapstructure:"dry_run" json:"dry_run"`
	TickIntervalSeconds  int           `mapstructure:"tick_interval_seconds" json:"tick_interval_seconds"`
	SustainWindowSeconds int           `mapstructure:"sustain_window_seconds" json:"sustain_window_seconds"`
	CooldownSeconds      int           `mapstructure:"cooldown_seconds" json:"cooldown_seconds"`
	AllowScaleUp         bool          `mapstructure:"allow_scale_up" json:"allow_scale_up"`
	AllowScaleDown       bool          `mapstructure:"allow_scale_down" json:"allow_scale_down"`
	ModelGroups          []ModelGroups `mapstructure:"model_groups" json:"model_groups"`
}

type ModelGroups struct {
	Name              string `mapstructure:"name" json:"name"`
	TTFTHotMs         int    `mapstructure:"ttft_hot_ms" json:"ttft_hot_ms"`
	ITLHotMs          int    `mapstructure:"itl_hot_ms" json:"itl_hot_ms"`
	MaxPrefillWorkers int    `mapstructure:"max_prefill_workers" json:"max_prefill_workers"`
	MaxDecodeWorkers  int    `mapstructure:"max_decode_workers" json:"max_decode_workers"`
	ExecutionScript   string `mapstructure:"execution_script" json:"execution_script"`
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
	if config.Scheduler.Port < 1 || config.Scheduler.Port > 65535 {
		return fmt.Errorf("scheduler.port should be between 1-65535")
	}
	if config.Scheduler.MetricsPort < 1 || config.Scheduler.MetricsPort > 65535 {
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

	if config.ProxyConfig.Enabled {
		if config.ProxyConfig.SchedulerURL == "" {
			return fmt.Errorf("proxy.scheduler_url cannot be empty")
		}
		if config.ProxyConfig.Port < 1 || config.ProxyConfig.Port > 65535 {
			return fmt.Errorf("proxy.port should be between 1-65535")
		}
	}
	if err := config.Rebalancing.Validate(); err != nil {
		return err
	}
	return nil
}

func (config RebalancingConfig) Validate() error {
	if !config.Enabled {
		return nil
	}
	if config.TickIntervalSeconds <= 0 {
		return fmt.Errorf("rebalancing.tick_interval_seconds must be greater than 0")
	}
	if config.SustainWindowSeconds <= 0 {
		return fmt.Errorf("rebalancing.sustain_window_seconds must be greater than 0")
	}
	if config.CooldownSeconds <= 0 {
		return fmt.Errorf("rebalancing.cooldown_seconds must be greater than 0")
	}
	if len(config.ModelGroups) == 0 {
		return fmt.Errorf("rebalancing.model_groups must have at least one model group when enabled")
	}

	seen := map[string]struct{}{}
	for i, group := range config.ModelGroups {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			return fmt.Errorf("rebalancing.model_groups[%d].name cannot be empty", i)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("rebalancing.model_groups[%d].name %q is duplicated", i, name)
		}
		seen[name] = struct{}{}

		if group.TTFTHotMs <= 0 {
			return fmt.Errorf("rebalancing.model_groups[%d].ttft_hot_ms must be greater than 0", i)
		}
		if group.ITLHotMs <= 0 {
			return fmt.Errorf("rebalancing.model_groups[%d].itl_hot_ms must be greater than 0", i)
		}
		if group.MaxPrefillWorkers <= 0 {
			return fmt.Errorf("rebalancing.model_groups[%d].max_prefill_workers must be greater than 0", i)
		}
		if group.MaxDecodeWorkers <= 0 {
			return fmt.Errorf("rebalancing.model_groups[%d].max_decode_workers must be greater than 0", i)
		}
	}
	return nil
}
