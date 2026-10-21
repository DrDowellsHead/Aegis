package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)
type roundTripFunc func(*http.Request)(*http.Response,error)
func (fn roundTripFunc) RoundTrip(req *http.Request)(*http.Response,error){return fn(req)}
func TestWebhookValidationAndPublicIP(t *testing.T) {
	for _,s:=range []string{
		"http://example.com","https://localhost/path",
		"https://127.0.0.1/path","https://10.0.0.3",
		"https://169.254.169.254/latest/meta-data",
		"https://example.com:8443/a","https://example.com/?token=leak",
		"https://user:pass@example.com/a","https://example.local/path",
	} {
		if ValidateWebhookURL(s)==nil {t.Errorf("unsafe endpoint accepted: %q",s)}
	}
	if err:=ValidateWebhookURL("https://hooks.example.com/alerts");err!=nil {t.Fatal(err)}
	for _,s:=range []string{
		"127.0.0.1","10.0.0.1","169.254.169.254","100.64.0.1",
		"192.168.1.2","198.18.0.1","::1","fc00::1","fe80::1",
	} {
		if publicIP(netip.MustParseAddr(s)){t.Errorf("private IP accepted: %s",s)}
	}
	if !publicIP(netip.MustParseAddr("8.8.8.8")) {t.Fatal("public IPv4 rejected")}
}
func TestWebhookSigningAndStableEventIdentity(t *testing.T) {
	s,_,err:=New(Config{WebhookURL:"https://hooks.example.com/events",
		WebhookSecret:strings.Repeat("s",32)})
	if err!=nil {t.Fatal(err)}
	job:=storage.NotificationJob{ID:"job1",EventID:42,Channel:"webhook",
		Target:"https://hooks.example.com/events",IncidentID:"incident1",
		AgentID:"agent1",Action:"opened",Title:"CPU high",Severity:"warning"}
	var count int
	s.http=&http.Client{Transport:roundTripFunc(func(req *http.Request)(*http.Response,error) {
		count++
		if req.URL.Host!="hooks.example.com" || req.Header.Get("Idempotency-Key")!="42-webhook" {
			t.Errorf("wrong delivery: %s",req.URL)
		}
		body,err:=io.ReadAll(req.Body)
		if err!=nil {t.Fatal(err)}
		mac:=hmac.New(sha256.New,[]byte(strings.Repeat("s",32)))
		mac.Write(body)
		expected:="sha256="+hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(req.Header.Get("X-Aegis-Signature")),[]byte(expected)){
			t.Fatal("HMAC signature mismatch")
		}
		return &http.Response{StatusCode:204,Body:io.NopCloser(strings.NewReader("")),Header:make(http.Header)},nil
	})}
	for i:=0;i<2;i++ {
		if res:=s.Deliver(context.Background(),job);res.Code!=""{t.Fatalf("delivery: %+v",res)}
	}
	if count!=2 {t.Fatal("unexpected number of attempts")}
}
func TestTelegramFailureClassification(t *testing.T) {
	s,_,err:=New(Config{TelegramChatID:"-12345",TelegramToken:"test-secret"})
	if err!=nil {t.Fatal(err)}
	s.http=&http.Client{Transport:roundTripFunc(func(req *http.Request)(*http.Response,error){
		if req.URL.Host!="api.telegram.org" || req.URL.Path!="/bottest-secret/sendMessage" {
			t.Fatalf("wrong Telegram destination: %v",req.URL)
		}
		return &http.Response{StatusCode:429,Body:io.NopCloser(strings.NewReader("{}")),Header:make(http.Header)},nil
	})}
	job:=storage.NotificationJob{Channel:"telegram",Target:"-12345",Action:"opened"}
	if got:=s.Deliver(context.Background(),job);got.Permanent || got.Code!="remote_retry" {
		t.Fatalf("429 should be retryable: %+v",got)
	}
	if got:=classifyHTTP(403);!got.Permanent{t.Fatal("403 should be permanent")}
	if got:=classifyHTTP(503);got.Permanent{t.Fatal("503 should be retried")}
}
func TestNotifyConfig(t *testing.T) {
	_,_,err:=New(Config{EmailTo:"ops@example.com",SMTPAddress:"smtp.example.com:465",
		SMTPFrom:"sender@example.com",SMTPUser:"u",SMTPPassword:"p"})
	if err!=nil{t.Fatal(err)}
	_,_,err=New(Config{WebhookURL:"https://hooks.example.com/x",WebhookSecret:"too-short"})
	if err==nil{t.Fatal("accepted weak webhook secret")}
	_,_,err=New(Config{TelegramChatID:"non-numeric",TelegramToken:"abc"})
	if err==nil{t.Fatal("accepted malformed chat ID")}
}
func TestBoundedPublicDialRefusesLoopback(t *testing.T) {
	_,err:=publicDial(context.Background(),"tcp",net.JoinHostPort("127.0.0.1","443"))
	if err==nil{t.Fatal("dialed loopback network")}
}
