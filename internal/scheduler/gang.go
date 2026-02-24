package scheduler

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

type GangState struct {
	GangID    string
	Size      int
	Pods      []*corev1.Pod
	CreatedAt time.Time
}

type GangCollector struct {
	timeout time.Duration
}

func NewGangCollector(timeout time.Duration) *GangCollector {
	return &GangCollector{timeout: timeout}
}

// Collect separates pending pods into ready gangs, standalone pods, and timed-out gang IDs.
// A gang is "ready" when all pods are present (len == gang-size).
// A gang is "timed out" when incomplete and the earliest pod's creation time exceeds the timeout.
// Standalone pods have no gang-id annotation.
func (gc *GangCollector) Collect(pods []*corev1.Pod) (readyGangs []GangState, standalonePods []*corev1.Pod, timedOutGangIDs []string) {
	gangMap := make(map[string]*GangState)

	for _, pod := range pods {
		gangID := GetGangIDFromPod(pod)
		if gangID == "" {
			standalonePods = append(standalonePods, pod)
			continue
		}

		state, exists := gangMap[gangID]
		if !exists {
			state = &GangState{
				GangID:    gangID,
				Size:      GetGangSizeFromPod(pod),
				CreatedAt: pod.CreationTimestamp.Time,
			}
			gangMap[gangID] = state
		}

		state.Pods = append(state.Pods, pod)

		// Track earliest creation time
		if pod.CreationTimestamp.Time.Before(state.CreatedAt) {
			state.CreatedAt = pod.CreationTimestamp.Time
		}
	}

	now := time.Now()
	for _, state := range gangMap {
		if len(state.Pods) >= state.Size {
			readyGangs = append(readyGangs, *state)
		} else if now.Sub(state.CreatedAt) > gc.timeout {
			timedOutGangIDs = append(timedOutGangIDs, state.GangID)
		}
		// else: incomplete but not timed out — skip, picked up next cycle
	}

	return readyGangs, standalonePods, timedOutGangIDs
}
