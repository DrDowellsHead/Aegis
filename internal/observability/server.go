package observability

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"
)

// RequireLoopback permits only numeric localhost addresses, not DNS names,
// wildcard binds or ambiguous zone identifiers.
func RequireLoopback(address string) error {
	host,port,err:=net.SplitHostPort(address)
	if err!=nil || port=="" || strings.Contains(host,"%") {return fmt.Errorf("diagnostics require a numeric loopback address and TCP port")}
	ip:=net.ParseIP(host)
	if ip==nil || !ip.IsLoopback(){return fmt.Errorf("diagnostics must bind to a numeric loopback IP")}
	return nil
}

// Handler does not expose profiles or metrics to unauthenticated callers.
// Neither credentials nor agent IDs appear in metric names or labels.
func Handler(secret string, metrics *Registry, stats func() sql.DBStats) (http.Handler,error) {
	if len(secret)<32 || metrics==nil{return nil,fmt.Errorf("diagnostics need a random token of at least 32 bytes and a metrics registry")}
	mux:=http.NewServeMux()
	mux.HandleFunc("GET /metrics",func(w http.ResponseWriter,_ *http.Request){
		w.Header().Set("Content-Type","text/plain; version=0.0.4; charset=utf-8")
		metrics.WritePrometheus(w,stats)
	})
	mux.HandleFunc("GET /debug/pprof/",pprof.Index)
	mux.HandleFunc("GET /debug/pprof/profile",pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/trace",pprof.Trace)
	mux.HandleFunc("GET /debug/pprof/symbol",pprof.Symbol)
	for _,name:=range []string{"heap","allocs","goroutine","threadcreate","block","mutex"}{
		mux.Handle("GET /debug/pprof/"+name,pprof.Handler(name))
	}
	return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		w.Header().Set("Cache-Control","no-store")
		w.Header().Set("X-Content-Type-Options","nosniff")
		host,_,err:=net.SplitHostPort(r.RemoteAddr)
		ip:=net.ParseIP(host)
		if err!=nil || ip==nil || !ip.IsLoopback(){http.Error(w,"local access only",http.StatusForbidden);return}
		provided:=r.Header.Get("Authorization")
		expected:="Bearer "+secret
		if subtle.ConstantTimeCompare([]byte(provided),[]byte(expected))!=1{
			w.Header().Set("WWW-Authenticate","Bearer")
			http.Error(w,"unauthorized",http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w,r)
	}),nil
}

// Serve owns the prebound listener and stops profiles when the parent
// receiver shuts down. It never binds to a public-facing interface.
func Serve(ctx context.Context, listener net.Listener, secret string, metrics *Registry, stats func() sql.DBStats, logger *slog.Logger) error {
	if listener==nil{return errors.New("diagnostic listener is required")}
	defer listener.Close()
	if err:=RequireLoopback(listener.Addr().String());err!=nil{return err}
	handler,err:=Handler(secret,metrics,stats)
	if err!=nil{return err}
	server:=&http.Server{
		Handler:handler, ReadHeaderTimeout:3*time.Second,
		ReadTimeout:10*time.Second, WriteTimeout:120*time.Second,
		IdleTimeout:30*time.Second, MaxHeaderBytes:8<<10,
	}
	done:=make(chan error,1)
	go func(){done<-server.Serve(listener)}()
	if logger!=nil{logger.Info("loopback diagnostics started","address",listener.Addr().String())}
	select{
	case err:=<-done:
		if errors.Is(err,http.ErrServerClosed){return nil}
		return fmt.Errorf("diagnostics server stopped unexpectedly: %w",err)
	case <-ctx.Done():
		shutdownCtx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
		defer cancel()
		if err:=server.Shutdown(shutdownCtx);err!=nil{_ = server.Close()}
		err:=<-done
		if err!=nil&&!errors.Is(err,http.ErrServerClosed){return err}
		return nil
	}
}
