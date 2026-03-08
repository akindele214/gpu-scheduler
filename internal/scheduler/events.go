package scheduler

type EventPublisher interface {
	Publish(eventType string, data interface{})
}

type noopPublisher struct{}

func (noopPublisher) Publish(string, interface{}) {}
