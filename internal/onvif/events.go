package onvif

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNoEvents means the camera offers no event service or refuses a
// PullPoint subscription: its motion has to come from somewhere else.
var ErrNoEvents = errors.New("onvif: camera offers no event subscription")

const (
	nsWSN = "http://docs.oasis-open.org/wsn/b-2"
	nsWSA = "http://www.w3.org/2005/08/addressing"

	pullTimeout = "PT5S"           // long-poll; below the HTTP client timeout
	renewEvery  = 30 * time.Second // subscriptions are asked to live 60 s
)

// WatchMotion subscribes to the camera's motion events with a PullPoint
// (ADR-0020) and calls report with the state of every motion message: the
// initial state, then each change. ready is called once the subscription
// works. It returns ErrNoEvents when there is no subscription to be had,
// ctx's error when ctx ends, and any other error when the subscription
// breaks (worth retrying).
func (c *Client) WatchMotion(ctx context.Context, ready func(), report func(on bool)) error {
	sub, err := c.subscribe(ctx)
	if err != nil {
		return err
	}
	defer c.unsubscribe(sub)
	ready()
	renewed := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Since(renewed) >= renewEvery {
			if err := c.subscriptionCall(ctx, sub, nsWSN+"/SubscriptionManager/RenewRequest",
				`<wsnt:Renew xmlns:wsnt="`+nsWSN+`"><wsnt:TerminationTime>PT60S</wsnt:TerminationTime></wsnt:Renew>`, "RenewResponse", nil); err != nil {
				return fmt.Errorf("onvif: renew: %w", err)
			}
			renewed = time.Now()
		}
		states, err := c.pull(ctx, sub)
		if err != nil {
			return err
		}
		for _, on := range states {
			report(on)
		}
	}
}

// subscribe creates a PullPoint subscription and returns its address.
func (c *Client) subscribe(ctx context.Context) (string, error) {
	if err := c.resolveServices(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	events := c.events
	c.mu.Unlock()
	if events == "" {
		return "", ErrNoEvents
	}
	var out struct {
		Address string `xml:"SubscriptionReference>Address"`
	}
	body := `<tev:CreatePullPointSubscription xmlns:tev="` + nsEvents + `"><tev:InitialTerminationTime>PT60S</tev:InitialTerminationTime></tev:CreatePullPointSubscription>`
	err := c.call(ctx, events, body, "CreatePullPointSubscriptionResponse", &out)
	var fault *Fault
	if errors.As(err, &fault) && unsupported(fault) {
		return "", fmt.Errorf("%w: %w", ErrNoEvents, err)
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Address) == "" {
		return "", fmt.Errorf("%w: no subscription address", ErrNoEvents)
	}
	return c.rebase(strings.TrimSpace(out.Address)), nil
}

// unsupported reports whether a fault says the camera cannot do what was
// asked, as opposed to rejecting this request (credentials, clock skew,
// arguments), which is worth retrying soon.
func unsupported(f *Fault) bool {
	switch f.Subcode {
	case "ActionNotSupported", "NotImplemented", "NoSuchService":
		return true
	}
	return false
}

// pull long-polls the subscription and returns the motion states it got.
func (c *Client) pull(ctx context.Context, sub string) ([]bool, error) {
	var out struct {
		Messages []notification `xml:"NotificationMessage"`
	}
	body := `<tev:PullMessages xmlns:tev="` + nsEvents + `"><tev:Timeout>` + pullTimeout + `</tev:Timeout><tev:MessageLimit>32</tev:MessageLimit></tev:PullMessages>`
	if err := c.subscriptionCall(ctx, sub, nsEvents+"/PullPointSubscription/PullMessagesRequest", body, "PullMessagesResponse", &out); err != nil {
		return nil, err
	}
	var states []bool
	for _, m := range out.Messages {
		if on, ok := m.motion(); ok {
			states = append(states, on)
		}
	}
	return states, nil
}

// unsubscribe ends the subscription, best effort: the camera drops it after
// its termination time anyway.
func (c *Client) unsubscribe(sub string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.subscriptionCall(ctx, sub, nsWSN+"/SubscriptionManager/UnsubscribeRequest", `<wsnt:Unsubscribe xmlns:wsnt="`+nsWSN+`"/>`, "UnsubscribeResponse", nil)
}

// subscriptionCall posts to a subscription with the WS-Addressing headers
// several cameras route by.
func (c *Client) subscriptionCall(ctx context.Context, sub, action, body, responseLocal string, out any) error {
	header := `<wsa:Action xmlns:wsa="` + nsWSA + `">` + action + `</wsa:Action><wsa:To xmlns:wsa="` + nsWSA + `">` + xmlEscape(sub) + `</wsa:To>`
	return c.callWith(ctx, sub, header, body, responseLocal, out)
}

// notification is one wsnt:NotificationMessage, read by local names.
type notification struct {
	Topic string `xml:"Topic"`
	Items []struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:"Value,attr"`
	} `xml:"Message>Message>Data>SimpleItem"`
}

// motion reads a motion state: a topic about motion (VideoSource/MotionAlarm,
// RuleEngine/CellMotionDetector/Motion and vendor variants) with a boolean
// IsMotion, State or Motion item.
func (n notification) motion() (on, ok bool) {
	if !strings.Contains(strings.ToLower(n.Topic), "motion") {
		return false, false
	}
	for _, it := range n.Items {
		switch it.Name {
		case "IsMotion", "State", "Motion":
			switch strings.ToLower(strings.TrimSpace(it.Value)) {
			case "true", "1":
				return true, true
			case "false", "0":
				return false, true
			}
		}
	}
	return false, false
}
