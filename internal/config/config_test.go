package config

import "testing"

func TestValidate_WorkflowsEnabled(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config with workflows",
			config: Config{
				Scheduler: SchedulerConfig{
					Name:        "gpu-scheduler",
					Port:        8888,
					MetricsPort: 9090,
					Mode:        "standalone",
				},
				Workflows: WorkflowConfig{
					Enabled:         true,
					AllowCustom:     true,
					DefaultPriority: 50,
					Types: []WorkFlowTypeConfig{
						{Name: "inference", Priority: 100, Preemptible: false},
						{Name: "training", Priority: 50, Preemptible: false},
					},
				},
				Queue: QueueConfig{
					MaxSize:       1000,
					DefaultPolicy: "binpack",
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			wantErr: false,
		},
		{
			name: "workflows enabled but no types",
			config: Config{
				Scheduler: SchedulerConfig{Name: "gpu-scheduler", Port: 8888, MetricsPort: 9090},
				Workflows: WorkflowConfig{
					Enabled:         true,
					DefaultPriority: 50,
					Types:           []WorkFlowTypeConfig{},
				},
				Queue:   QueueConfig{MaxSize: 1000, DefaultPolicy: "binpack"},
				Logging: LoggingConfig{Level: "info", Format: "json"},
			},
			wantErr: true,
			errMsg:  "workflows.types must have at least one workflow when enabled",
		},
		{
			name: "workflow with empty name",
			config: Config{
				Scheduler: SchedulerConfig{Name: "gpu-scheduler", Port: 8888, MetricsPort: 9090},
				Workflows: WorkflowConfig{
					Enabled:         true,
					DefaultPriority: 50,
					Types: []WorkFlowTypeConfig{
						{Name: "", Priority: 50},
					},
				},
				Queue:   QueueConfig{MaxSize: 1000, DefaultPolicy: "binpack"},
				Logging: LoggingConfig{Level: "info", Format: "json"},
			},
			wantErr: true,
			errMsg:  "workflow at index [0] name cannot be empty",
		},
		{
			name: "workflow priority out of range",
			config: Config{
				Scheduler: SchedulerConfig{Name: "gpu-scheduler", Port: 8888, MetricsPort: 9090},
				Workflows: WorkflowConfig{
					Enabled:         true,
					DefaultPriority: 50,
					Types: []WorkFlowTypeConfig{
						{Name: "training", Priority: 150},
					},
				},
				Queue:   QueueConfig{MaxSize: 1000, DefaultPolicy: "binpack"},
				Logging: LoggingConfig{Level: "info", Format: "json"},
			},
			wantErr: true,
			errMsg:  "workflow 'training' priority must be between 0-100",
		},
		{
			name: "default priority out of range",
			config: Config{
				Scheduler: SchedulerConfig{Name: "gpu-scheduler", Port: 8888, MetricsPort: 9090},
				Workflows: WorkflowConfig{
					Enabled:         true,
					DefaultPriority: 150,
					Types: []WorkFlowTypeConfig{
						{Name: "training", Priority: 50},
					},
				},
				Queue:   QueueConfig{MaxSize: 1000, DefaultPolicy: "binpack"},
				Logging: LoggingConfig{Level: "info", Format: "json"},
			},
			wantErr: true,
			errMsg:  "workflows.defaultPriority must be between 0-100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if err.Error() != tt.errMsg {
					t.Errorf("expected error '%s', got '%s'", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidate_Rebalancing(t *testing.T) {
	validConfig := func() Config {
		return Config{
			Scheduler: SchedulerConfig{
				Name:        "gpu-scheduler",
				Port:        8888,
				MetricsPort: 9090,
			},
			Queue: QueueConfig{
				MaxSize:       1000,
				DefaultPolicy: "fifo",
			},
			Logging: LoggingConfig{
				Level:  "info",
				Format: "json",
			},
			Rebalancing: RebalancingConfig{
				Enabled:              true,
				DryRun:               true,
				TickIntervalSeconds:  5,
				SustainWindowSeconds: 30,
				CooldownSeconds:      90,
				AllowScaleUp:         true,
				AllowScaleDown:       false,
				ModelGroups: []ModelGroups{
					{
						Name:              "Qwen/Qwen2.5-7B-Instruct",
						TTFTHotMs:         800,
						ITLHotMs:          120,
						MaxPrefillWorkers: 2,
						MaxDecodeWorkers:  2,
					},
				},
			},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid rebalancing config",
			mutate:  func(*Config) {},
			wantErr: false,
		},
		{
			name: "disabled rebalancing allows empty fields",
			mutate: func(c *Config) {
				c.Rebalancing = RebalancingConfig{}
			},
			wantErr: false,
		},
		{
			name: "enabled requires positive tick interval",
			mutate: func(c *Config) {
				c.Rebalancing.TickIntervalSeconds = 0
			},
			wantErr: true,
			errMsg:  "rebalancing.tick_interval_seconds must be greater than 0",
		},
		{
			name: "enabled requires positive sustain window",
			mutate: func(c *Config) {
				c.Rebalancing.SustainWindowSeconds = 0
			},
			wantErr: true,
			errMsg:  "rebalancing.sustain_window_seconds must be greater than 0",
		},
		{
			name: "enabled requires positive cooldown",
			mutate: func(c *Config) {
				c.Rebalancing.CooldownSeconds = 0
			},
			wantErr: true,
			errMsg:  "rebalancing.cooldown_seconds must be greater than 0",
		},
		{
			name: "enabled requires model groups",
			mutate: func(c *Config) {
				c.Rebalancing.ModelGroups = nil
			},
			wantErr: true,
			errMsg:  "rebalancing.model_groups must have at least one model group when enabled",
		},
		{
			name: "model group name cannot be empty",
			mutate: func(c *Config) {
				c.Rebalancing.ModelGroups[0].Name = " "
			},
			wantErr: true,
			errMsg:  "rebalancing.model_groups[0].name cannot be empty",
		},
		{
			name: "model group name cannot be duplicated",
			mutate: func(c *Config) {
				c.Rebalancing.ModelGroups = append(c.Rebalancing.ModelGroups, c.Rebalancing.ModelGroups[0])
			},
			wantErr: true,
			errMsg:  "rebalancing.model_groups[1].name \"Qwen/Qwen2.5-7B-Instruct\" is duplicated",
		},
		{
			name: "ttft threshold must be positive",
			mutate: func(c *Config) {
				c.Rebalancing.ModelGroups[0].TTFTHotMs = 0
			},
			wantErr: true,
			errMsg:  "rebalancing.model_groups[0].ttft_hot_ms must be greater than 0",
		},
		{
			name: "itl threshold must be positive",
			mutate: func(c *Config) {
				c.Rebalancing.ModelGroups[0].ITLHotMs = 0
			},
			wantErr: true,
			errMsg:  "rebalancing.model_groups[0].itl_hot_ms must be greater than 0",
		},
		{
			name: "max prefill workers must be positive",
			mutate: func(c *Config) {
				c.Rebalancing.ModelGroups[0].MaxPrefillWorkers = 0
			},
			wantErr: true,
			errMsg:  "rebalancing.model_groups[0].max_prefill_workers must be greater than 0",
		},
		{
			name: "max decode workers must be positive",
			mutate: func(c *Config) {
				c.Rebalancing.ModelGroups[0].MaxDecodeWorkers = 0
			},
			wantErr: true,
			errMsg:  "rebalancing.model_groups[0].max_decode_workers must be greater than 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if err.Error() != tt.errMsg {
					t.Errorf("expected error '%s', got '%s'", tt.errMsg, err.Error())
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
