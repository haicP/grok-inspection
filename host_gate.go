package main

// hostCallGate bounds concurrent CGO host callbacks (host.http.do / host.auth.*).
// Soft timeouts in probe.go may return early, but abandoned calls still hold a
// slot until the host returns. That prevents timed-out workers from spawning
// unbounded OS threads when upstream hangs.
var hostCallGate = make(chan struct{}, maxWorkers)

func acquireHostCall() {
	hostCallGate <- struct{}{}
}

func releaseHostCall() {
	<-hostCallGate
}

func hostCallInflight() int {
	return len(hostCallGate)
}

func hostCallCapacity() int {
	return cap(hostCallGate)
}
