package agent

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type heartbeatSender struct {
	mu sync.Mutex
	calls int
	notify chan struct{}
}
func (s *heartbeatSender) Heartbeat(context.Context) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	select { case s.notify<-struct{}{}: default: }
	return nil
}
func TestHeartbeatLoopStartsImmediatelyAndStops(t *testing.T) {
	ctx,cancel:=context.WithCancel(context.Background())
	done:=make(chan error,1)
	s:=&heartbeatSender{notify:make(chan struct{},8)}
	go func(){done<-RunHeartbeat(ctx,s,time.Second,200*time.Millisecond,
		slog.New(slog.NewTextHandler(io.Discard,nil)))}()
	select { case <-s.notify:
	case <-time.After(500*time.Millisecond): cancel(); <-done; t.Fatal("initial heartbeat not sent promptly") }
	cancel()
	select { case err:=<-done: if err!=nil{t.Fatal(err)}
	case <-time.After(time.Second): t.Fatal("heartbeat goroutine survived cancellation") }
	s.mu.Lock(); n:=s.calls; s.mu.Unlock()
	if n!=1 {t.Fatalf("unexpected heartbeats after shutdown: %d",n)}
}
func TestHeartbeatLoopRejectsUnsafeSettings(t *testing.T) {
	log:=slog.New(slog.NewTextHandler(io.Discard,nil))
	s:=&heartbeatSender{notify:make(chan struct{},1)}
	for _,q:=range []struct{i,t time.Duration}{
		{100*time.Millisecond,time.Second},{2*time.Hour,time.Second},
		{time.Second,0},{time.Second,2*time.Minute},
	} {
		if err:=RunHeartbeat(context.Background(),s,q.i,q.t,log);err==nil{
			t.Fatalf("invalid heartbeat limits accepted: %+v",q)
		}
	}
}
