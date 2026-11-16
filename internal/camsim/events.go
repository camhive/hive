package camsim

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// pullPoints is the mock ONVIF event service (ADR-0020): PullPoint
// subscriptions that report CellMotionDetector/Motion for every camera with
// a Motion period, on the Camera.Moving schedule. The first pull returns
// each camera's initial state; later pulls wait until a state changes.
type pullPoints struct {
	cfg   *Config
	start time.Time

	mu   sync.Mutex
	next int
	subs map[string][]bool // last reported state per motion camera
}

func newPullPoints(cfg *Config) *pullPoints {
	return &pullPoints{cfg: cfg, start: time.Now(), subs: map[string][]bool{}}
}

// states is the current motion state of every motion camera, in order.
func (p *pullPoints) states() []bool {
	since := time.Since(p.start)
	cams := p.motionCameras()
	out := make([]bool, 0, len(cams))
	for _, cam := range cams {
		out = append(out, cam.Moving(since))
	}
	return out
}

func (p *pullPoints) motionCameras() []Camera {
	var out []Camera
	for _, cam := range p.cfg.Cameras {
		if cam.Motion > 0 {
			out = append(out, cam)
		}
	}
	return out
}

func (p *pullPoints) subscribe() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	id := fmt.Sprint(p.next)
	p.subs[id] = nil
	return id
}

// pull waits up to wait for a state change on subscription id and returns
// the messages to send; ok is false for an unknown subscription.
func (p *pullPoints) pull(id string, wait time.Duration) (string, bool) {
	deadline := time.Now().Add(wait)
	for {
		p.mu.Lock()
		last, ok := p.subs[id]
		if !ok {
			p.mu.Unlock()
			return "", false
		}
		now := p.states()
		msgs := p.changes(last, now)
		if msgs != "" || !time.Now().Before(deadline) {
			p.subs[id] = now
			p.mu.Unlock()
			return msgs, true
		}
		p.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
}

// changes renders a NotificationMessage for every camera whose state differs
// from last (all of them when last is nil: the initial state).
func (p *pullPoints) changes(last, now []bool) string {
	var b strings.Builder
	at := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	for i, cam := range p.motionCameras() {
		if last != nil && last[i] == now[i] {
			continue
		}
		op := "Changed"
		if last == nil {
			op = "Initialized"
		}
		fmt.Fprintf(&b, `<wsnt:NotificationMessage><wsnt:Topic Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">tns1:RuleEngine/CellMotionDetector/Motion</wsnt:Topic>`+
			`<wsnt:Message><tt:Message UtcTime="%s" PropertyOperation="%s"><tt:Source><tt:SimpleItem Name="VideoSourceConfigurationToken" Value="vsc_%s"/></tt:Source>`+
			`<tt:Data><tt:SimpleItem Name="IsMotion" Value="%v"/></tt:Data></tt:Message></wsnt:Message></wsnt:NotificationMessage>`, at, op, cam.Name, now[i])
	}
	return b.String()
}

func (p *pullPoints) unsubscribe(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.subs, id)
}

// register serves the event service and its subscriptions on mux, unless
// the camera has none.
func (p *pullPoints) register(mux *http.ServeMux) {
	if p.cfg.NoEvents {
		return
	}
	mux.HandleFunc("POST /onvif/events_service", p.cfg.soapEndpoint(p.handleEvents))
	mux.HandleFunc("POST /onvif/subscription/{id}", func(w http.ResponseWriter, r *http.Request) {
		p.cfg.soapEndpoint(p.handleSubscription(r.PathValue("id")))(w, r)
	})
}

// handleEvents serves CreatePullPointSubscription on the events service.
func (p *pullPoints) handleEvents(w http.ResponseWriter, req soapRequest) {
	if req.Action != "CreatePullPointSubscription" {
		writeFault(w, "ActionNotSupported", req.Action)
		return
	}
	id := p.subscribe()
	now := time.Now().UTC()
	writeSOAP(w, http.StatusOK, fmt.Sprintf(`<tev:CreatePullPointSubscriptionResponse><tev:SubscriptionReference><wsa:Address>%s</wsa:Address></tev:SubscriptionReference>`+
		`<wsnt:CurrentTime>%s</wsnt:CurrentTime><wsnt:TerminationTime>%s</wsnt:TerminationTime></tev:CreatePullPointSubscriptionResponse>`,
		p.cfg.subscriptionURL(id), now.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339)))
}

// handleSubscription serves PullMessages, Renew and Unsubscribe.
func (p *pullPoints) handleSubscription(id string) func(http.ResponseWriter, soapRequest) {
	return func(w http.ResponseWriter, req soapRequest) {
		now := time.Now().UTC()
		switch req.Action {
		case "PullMessages":
			msgs, ok := p.pull(id, pullWait(textOf(req.Body, "Timeout")))
			if !ok {
				writeFault(w, "InvalidArgVal", "unknown subscription")
				return
			}
			writeSOAP(w, http.StatusOK, fmt.Sprintf(`<tev:PullMessagesResponse><tev:CurrentTime>%s</tev:CurrentTime><tev:TerminationTime>%s</tev:TerminationTime>%s</tev:PullMessagesResponse>`,
				now.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339), msgs))
		case "Renew":
			writeSOAP(w, http.StatusOK, fmt.Sprintf(`<wsnt:RenewResponse><wsnt:TerminationTime>%s</wsnt:TerminationTime></wsnt:RenewResponse>`, now.Add(time.Minute).Format(time.RFC3339)))
		case "Unsubscribe":
			p.unsubscribe(id)
			writeSOAP(w, http.StatusOK, `<wsnt:UnsubscribeResponse/>`)
		default:
			writeFault(w, "ActionNotSupported", req.Action)
		}
	}
}

// pullWait reads an xs:duration like PT5S; the mock waits at most 5 s.
func pullWait(d string) time.Duration {
	var s int
	if _, err := fmt.Sscanf(d, "PT%dS", &s); err != nil || s <= 0 || s > 5 {
		s = 5
	}
	return time.Duration(s) * time.Second
}
