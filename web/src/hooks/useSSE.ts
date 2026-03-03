import type { SSEEvent } from '@/types/api';
import { useEffect, useRef } from 'react';
import { subscribeEvents } from './useApi';

export function useSSE(eventTypes: string[], onEvent: () => void) {
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  useEffect(() => {
    const es = subscribeEvents((event: SSEEvent) => {
      if (eventTypes.includes(event.sse_event_type)) {
        onEventRef.current();
      }
    });
    return () => es.close();
  }, [eventTypes.join(',')]);
}

export function useSSEWithData<T>(
  eventTypes: string[],
  onEvent: (data: T) => void,
) {
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  useEffect(() => {
    const es = subscribeEvents((event: SSEEvent) => {
      if (eventTypes.includes(event.sse_event_type)) {
        onEventRef.current(event.data as T);
      }
    });
    return () => es.close();
  }, [eventTypes.join(',')]);
}
