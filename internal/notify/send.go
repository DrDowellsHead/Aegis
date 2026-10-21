// Package notify delivers committed outbox jobs. No network calls occur
// inside incident transactions; retries are at-least-once, not exactly-once.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

type Config struct {
	EmailTo string
	SMTPAddress string // host:port, preferably implicit TLS on 465
	SMTPFrom string
	SMTPUser string
	SMTPPassword string
	TelegramChatID string
	TelegramToken string
	WebhookURL string
	WebhookSecret string
}
type Result struct {
	Permanent bool
	Code string // secret-free, bounded classifier; never expose response bodies
}
type Sender struct {
	cfg Config
	http *http.Client
}
func New(cfg Config) (*Sender,[]storage.NotificationRoute,error) {
	var routes []storage.NotificationRoute
	if cfg.EmailTo!="" {
		if _,err:=mail.ParseAddress(cfg.SMTPFrom);err!=nil{return nil,nil,fmt.Errorf("invalid sender email")}
		host,_,err:=net.SplitHostPort(cfg.SMTPAddress)
		if err!=nil || host=="" || cfg.SMTPUser=="" || cfg.SMTPPassword=="" {
			return nil,nil,fmt.Errorf("email requires SMTP address, sender and credentials")
		}
		routes=append(routes,storage.NotificationRoute{Channel:"email",Target:cfg.EmailTo})
	}
	if cfg.TelegramChatID!="" {
		if cfg.TelegramToken=="" {return nil,nil,fmt.Errorf("Telegram token required")}
		routes=append(routes,storage.NotificationRoute{Channel:"telegram",Target:cfg.TelegramChatID})
	}
	if cfg.WebhookURL!="" {
		if len(cfg.WebhookSecret)<32 {return nil,nil,fmt.Errorf("webhook HMAC secret must be at least 32 bytes")}
		if err:=ValidateWebhookURL(cfg.WebhookURL);err!=nil{return nil,nil,err}
		routes=append(routes,storage.NotificationRoute{Channel:"webhook",Target:cfg.WebhookURL})
	}
	if len(routes)==0 {return nil,nil,fmt.Errorf("configure at least one notification channel")}
	// Reject invalid routing targets before any SQL changes.
	for _,r:=range routes {
		switch r.Channel {
		case "email":
			a,err:=mail.ParseAddress(r.Target);if err!=nil || a.Address!=r.Target {return nil,nil,fmt.Errorf("invalid recipient")}
		case "telegram":
			if len(r.Target)>20 || r.Target=="" {return nil,nil,fmt.Errorf("invalid chat ID")}
			for i,ch:=range r.Target {
				if ch=='-' && i==0 {continue}
				if ch<'0'||ch>'9' {return nil,nil,fmt.Errorf("invalid chat ID")}
			}
		}
	}
	transport:=&http.Transport{
		Proxy:nil,ForceAttemptHTTP2:true,
		TLSHandshakeTimeout:5*time.Second,
		ResponseHeaderTimeout:10*time.Second,
		DialContext:publicDial,
	}
	return &Sender{cfg:cfg,http:&http.Client{
		Timeout:12*time.Second,Transport:transport,
		CheckRedirect:func(*http.Request,[]*http.Request) error {
			return http.ErrUseLastResponse
		},
	}},routes,nil
}

// Block SSRF to local/private/reserved IPs, including DNS-rebinding.
// This is applied to the actual TCP dial, not just a preliminary DNS check.
func publicIP(ip netip.Addr) bool {
	if ip.Is4In6(){ip=ip.Unmap()}
	if !ip.IsValid() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || !ip.IsGlobalUnicast() {return false}
	blacklist:=[]string{
		"0.0.0.0/8","100.64.0.0/10","169.254.0.0/16",
		"192.0.0.0/24","192.0.2.0/24","198.18.0.0/15",
		"198.51.100.0/24","203.0.113.0/24","224.0.0.0/4",
		"240.0.0.0/4","2001:db8::/32","fc00::/7","fe80::/10",
	}
	for _,s:=range blacklist {p:=netip.MustParsePrefix(s);if p.Contains(ip){return false}}
	return true
}
func publicDial(ctx context.Context,network,address string)(net.Conn,error) {
	host,port,err:=net.SplitHostPort(address)
	if err!=nil {return nil,err}
	if port!="443" {return nil,errors.New("outbound HTTPS must use port 443")}
	addrs,err:=net.DefaultResolver.LookupNetIP(ctx,"ip",host)
	if err!=nil{return nil,errors.New("could not resolve public host")}
	dialer:=&net.Dialer{Timeout:5*time.Second}
	for _,ip:=range addrs {
		if !publicIP(ip) {continue}
		conn,err:=dialer.DialContext(ctx,"tcp",net.JoinHostPort(ip.String(),port))
		if err==nil{return conn,nil}
	}
	return nil,errors.New("public network connection unavailable")
}
func ValidateWebhookURL(raw string) error {
	u,err:=url.Parse(raw)
	if err!=nil || u.Scheme!="https" || u.Hostname()=="" || u.User!=nil ||
		u.Opaque!="" || u.RawQuery!="" || u.Fragment!="" || u.Port()!="" && u.Port()!="443" {
		return errors.New("webhook must be a public HTTPS URL on port 443 without credentials, query or fragment")
	}
	if ip,err:=netip.ParseAddr(u.Hostname());err==nil && !publicIP(ip) {
		return errors.New("webhook cannot target private or reserved addresses")
	}
	if strings.HasSuffix(strings.ToLower(u.Hostname()),".local") ||
		!strings.Contains(u.Hostname(),".") {return errors.New("webhook requires a public DNS hostname")}
	return nil
}

type Event struct {
	EventID int64 `json:"event_id"`
	IncidentID string `json:"incident_id"`
	AgentID string `json:"agent_id"`
	Kind string `json:"kind"`
	Action string `json:"action"`
	Title string `json:"title"`
	Severity string `json:"severity"`
}
func makeEvent(job storage.NotificationJob) Event {
	return Event{EventID:job.EventID,IncidentID:job.IncidentID,
		AgentID:job.AgentID,Kind:job.Kind,Action:job.Action,
		Title:job.Title,Severity:job.Severity}
}
func (s *Sender) Deliver(ctx context.Context,job storage.NotificationJob) Result {
	if s==nil {return Result{Permanent:true,Code:"sender_unavailable"}}
	switch job.Channel {
	case "webhook":
		if job.Target!=s.cfg.WebhookURL {return Result{Permanent:true,Code:"route_changed"}}
		return s.webhook(ctx,job)
	case "telegram":
		if job.Target!=s.cfg.TelegramChatID {return Result{Permanent:true,Code:"route_changed"}}
		return s.telegram(ctx,job)
	case "email":
		if job.Target!=s.cfg.EmailTo {return Result{Permanent:true,Code:"route_changed"}}
		return s.email(ctx,job)
	default:
		return Result{Permanent:true,Code:"unknown_channel"}
	}
}
func classifyHTTP(status int) Result {
	switch {
	case status>=200 && status<300: return Result{}
	case status==429 || status>=500: return Result{Code:"remote_retry"}
	default: return Result{Permanent:true,Code:"remote_rejected"}
	}
}
func (s *Sender) webhook(ctx context.Context,job storage.NotificationJob) Result {
	if err:=ValidateWebhookURL(job.Target);err!=nil{return Result{Permanent:true,Code:"invalid_destination"}}
	body,err:=json.Marshal(makeEvent(job))
	if err!=nil{return Result{Permanent:true,Code:"encoding_failed"}}
	req,err:=http.NewRequestWithContext(ctx,http.MethodPost,job.Target,bytes.NewReader(body))
	if err!=nil{return Result{Permanent:true,Code:"invalid_destination"}}
	mac:=hmac.New(sha256.New,[]byte(s.cfg.WebhookSecret))
	mac.Write(body)
	req.Header.Set("Content-Type","application/json")
	req.Header.Set("X-Aegis-Event-ID",strconv.FormatInt(job.EventID,10))
	req.Header.Set("X-Aegis-Signature","sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Idempotency-Key",strconv.FormatInt(job.EventID,10)+"-"+job.Channel)
	resp,err:=s.http.Do(req)
	if err!=nil{return Result{Code:"network_error"}}
	io.Copy(io.Discard,io.LimitReader(resp.Body,4096))
	resp.Body.Close()
	return classifyHTTP(resp.StatusCode)
}
func (s *Sender) telegram(ctx context.Context,job storage.NotificationJob) Result {
	message:=fmt.Sprintf("Aegis %s [%s]\n%s\nAgent: %s\nIncident: %s",
		job.Action,job.Severity,job.Title,job.AgentID,job.IncidentID)
	payload,_:=json.Marshal(map[string]string{"chat_id":job.Target,"text":message})
	// The destination is a hard-coded trusted Telegram API endpoint.
	endpoint:="https://api.telegram.org/bot"+s.cfg.TelegramToken+"/sendMessage"
	req,err:=http.NewRequestWithContext(ctx,http.MethodPost,endpoint,bytes.NewReader(payload))
	if err!=nil{return Result{Permanent:true,Code:"invalid_telegram_request"}}
	req.Header.Set("Content-Type","application/json")
	resp,err:=s.http.Do(req)
	if err!=nil{return Result{Code:"network_error"}}
	io.Copy(io.Discard,io.LimitReader(resp.Body,4096))
	resp.Body.Close()
	return classifyHTTP(resp.StatusCode)
}
func (s *Sender) email(ctx context.Context,job storage.NotificationJob) Result {
	host,_,err:=net.SplitHostPort(s.cfg.SMTPAddress)
	if err!=nil {return Result{Permanent:true,Code:"invalid_smtp_address"}}
	// Implicit SMTP over TLS. Never allow plaintext credentials or
	// silently downgrade to STARTTLS-less SMTP.
	dialer:=tls.Dialer{NetDialer:&net.Dialer{Timeout:5*time.Second},
		Config:&tls.Config{MinVersion:tls.VersionTLS12,ServerName:host}}
	conn,err:=dialer.DialContext(ctx,"tcp",s.cfg.SMTPAddress)
	if err!=nil{return Result{Code:"smtp_connect"}}
	defer conn.Close()
	deadline:=time.Now().Add(10*time.Second)
	if d,ok:=ctx.Deadline();ok && d.Before(deadline) {deadline=d}
	_ = conn.SetDeadline(deadline)
	client,err:=smtp.NewClient(conn,host)
	if err!=nil{return Result{Code:"smtp_protocol"}}
	defer client.Close()
	auth:=smtp.PlainAuth("",s.cfg.SMTPUser,s.cfg.SMTPPassword,host)
	if err=client.Auth(auth);err!=nil{return Result{Permanent:true,Code:"smtp_auth"}}
	if err=client.Mail(s.cfg.SMTPFrom);err!=nil{return Result{Permanent:true,Code:"smtp_sender"}}
	if err=client.Rcpt(job.Target);err!=nil{return Result{Permanent:true,Code:"smtp_recipient"}}
	writer,err:=client.Data()
	if err!=nil{return Result{Code:"smtp_data"}}
	subject:="Aegis incident "+job.Action
	body:=fmt.Sprintf("Event: %d\r\nIncident: %s\r\nAgent: %s\r\nKind: %s\r\nSeverity: %s\r\nTitle: %s\r\n",
		job.EventID,job.IncidentID,job.AgentID,job.Kind,job.Severity,
		strings.Map(func(r rune)rune{if r=='\r'||r=='\n' {return ' '};return r},job.Title))
	msg:=fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%d.aegis@localhost>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		s.cfg.SMTPFrom,job.Target,subject,job.EventID,body)
	_,err=io.WriteString(writer,msg)
	if err!=nil {writer.Close();return Result{Code:"smtp_write"}}
	if err=writer.Close();err!=nil{return Result{Code:"smtp_complete"}}
	return Result{}
}
