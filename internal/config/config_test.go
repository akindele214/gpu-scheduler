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
