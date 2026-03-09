package dashboard

import (
	"strings"
	"sync"
)

type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	Raw       string `json:"raw"`
}

type LogBuffer struct {
	mu       sync.Mutex
	entries  []LogEntry
	size     int
	pos      int
	count    int
	eventBus *EventBus
}

func NewLogBuffer(size int, eventBus *EventBus) *LogBuffer {
	return &LogBuffer{
		entries:  make([]LogEntry, size),
		size:     size,
		eventBus: eventBus,
	}
}

func (lb *LogBuffer) Write(p []byte) (int, error) {
	convertedStrings := strings.Split(string(p), "\n")
	if len(convertedStrings) == 0 {
		return 0, nil
	}
	for _, line := range convertedStrings {
		if line == "" {
			continue
		}
		lb.mu.Lock()
		entry := parseLine(line)
		lb.entries[lb.pos%lb.size] = entry
		lb.pos += 1
		lb.count += 1
		lb.mu.Unlock()
		lb.eventBus.Publish(SSEEvent{Type: "scheduler-log", Data: entry})
	}
	return len(p), nil
}

func (lb *LogBuffer) GetRecent(limit int, category string) []LogEntry {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	total := min(lb.count, lb.size)
	var index int
	if lb.count <= lb.size {
		index = 0
	} else if lb.count > lb.size {
		index = lb.pos % lb.size
	}
	returnLogs := []LogEntry{}
	for i := 0; i < total; i++ {
		entry := lb.entries[(index+i)%lb.size]
		if category != "" && entry.Category != category {
			continue
		}
		returnLogs = append(returnLogs, entry)
		if len(returnLogs) >= limit {
			break
		}
	}

	return returnLogs
}

func parseLine(line string) LogEntry {
	openBracket := strings.Index(line, "[")
	closeBracket := strings.Index(line, "]")
	var category, timestamp, message string
	if openBracket != -1 && closeBracket != -1 && closeBracket > openBracket {
		category = line[openBracket+1 : closeBracket] // slice between the brackets
		timestamp = strings.TrimSpace(line[:openBracket])
		message = strings.TrimSpace(line[closeBracket+1:])
	} else {
		category = "GENERAL"
		message = line
	}

	return LogEntry{
		Raw:       line,
		Category:  category,
		Timestamp: timestamp,
		Message:   message,
	}
}
