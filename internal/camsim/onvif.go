package camsim

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// profileToken is the ONVIF media profile token of a camera stream.
func profileToken(cam Camera, s Stream) string {
	return "prof_" + cam.Name + "_" + string(s)
}

// soapRequest is the minimum we need from an incoming SOAP envelope: the local
// name of the first body element (the action) and the raw body for parameters.
type soapRequest struct {
	Action string
	Body   string
}

func parseSOAP(r io.Reader) (soapRequest, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return soapRequest{}, err
	}
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	inBody := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return soapRequest{}, fmt.Errorf("soap: no body action: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "Body" {
			inBody = true
			continue
		}
		if inBody {
			return soapRequest{Action: se.Name.Local, Body: string(raw)}, nil
		}
	}
}

// textOf returns the text of the first element with the given local name.
func textOf(body, local string) string {
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == local {
			var v string
			_ = dec.DecodeElement(&v, &se)
			return strings.TrimSpace(v)
		}
	}
}

const (
	envOpen = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"` +
		` xmlns:tds="http://www.onvif.org/ver10/device/wsdl"` +
		` xmlns:trt="http://www.onvif.org/ver10/media/wsdl"` +
		` xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body>`
	envClose = `</s:Body></s:Envelope>`
)

func writeSOAP(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, envOpen+body+envClose)
}

func writeFault(w http.ResponseWriter, subcode, reason string) {
	writeSOAP(w, http.StatusBadRequest, fmt.Sprintf(
		`<s:Fault><s:Code><s:Value>s:Sender</s:Value><s:Subcode><s:Value>ter:%s</s:Value></s:Subcode></s:Code>`+
			`<s:Reason><s:Text xml:lang="en">%s</s:Text></s:Reason></s:Fault>`, subcode, reason))
}

// newONVIFHandler serves the ONVIF device, media and PTZ services for cfg.
func newONVIFHandler(cfg *Config) http.Handler {
	return newONVIFHandlerWithPTZ(cfg, newPTZLog())
}

// newONVIFHandlerWithPTZ is newONVIFHandler with an externally visible PTZ log.
func newONVIFHandlerWithPTZ(cfg *Config, ptz *ptzLog) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /onvif/device_service", cfg.soapEndpoint(func(w http.ResponseWriter, req soapRequest) {
		switch req.Action {
		case "GetDeviceInformation":
			writeSOAP(w, http.StatusOK, deviceInformation(cfg))
		case "GetSystemDateAndTime":
			writeSOAP(w, http.StatusOK, systemDateAndTime(time.Now().UTC()))
		case "SetSystemDateAndTime":
			writeSOAP(w, http.StatusOK, `<tds:SetSystemDateAndTimeResponse/>`)
		case "GetCapabilities":
			writeSOAP(w, http.StatusOK, capabilities(cfg))
		case "GetServices":
			writeSOAP(w, http.StatusOK, services(cfg))
		default:
			writeFault(w, "ActionNotSupported", req.Action)
		}
	}))
	mux.HandleFunc("POST /onvif/media_service", cfg.soapEndpoint(func(w http.ResponseWriter, req soapRequest) {
		switch req.Action {
		case "GetProfiles":
			writeSOAP(w, http.StatusOK, profiles(cfg))
		case "GetStreamUri":
			cam, s, ok := lookupProfile(cfg, textOf(req.Body, "ProfileToken"))
			if !ok {
				writeFault(w, "NoProfile", "profile token does not exist")
				return
			}
			writeSOAP(w, http.StatusOK, fmt.Sprintf(
				`<trt:GetStreamUriResponse><trt:MediaUri><tt:Uri>%s</tt:Uri>`+
					`<tt:InvalidAfterConnect>false</tt:InvalidAfterConnect><tt:InvalidAfterReboot>false</tt:InvalidAfterReboot>`+
					`<tt:Timeout>PT60S</tt:Timeout></trt:MediaUri></trt:GetStreamUriResponse>`, cfg.streamURI(cam, s)))
		default:
			writeFault(w, "ActionNotSupported", req.Action)
		}
	}))
	mux.HandleFunc("POST /onvif/ptz_service", cfg.soapEndpoint(func(w http.ResponseWriter, req soapRequest) {
		handlePTZ(cfg, ptz, w, req)
	}))
	return mux
}

// soapEndpoint parses the envelope, applies the profile's delay and the
// optional WS-UsernameToken check, then dispatches.
func (c *Config) soapEndpoint(dispatch func(http.ResponseWriter, soapRequest)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := parseSOAP(r.Body)
		if err != nil {
			writeFault(w, "WellFormed", err.Error())
			return
		}
		if c.Profile == Quirky {
			time.Sleep(c.quirkDelay)
		}
		if c.Username != "" {
			tok, ok := parseUsernameToken(req.Body)
			if !ok || !verifyUsernameToken(tok, c.Username, c.Password) {
				writeSOAP(w, http.StatusUnauthorized, `<s:Fault><s:Code><s:Value>s:Sender</s:Value><s:Subcode><s:Value>ter:NotAuthorized</s:Value></s:Subcode></s:Code>`+
					`<s:Reason><s:Text xml:lang="en">Sender not authorized</s:Text></s:Reason></s:Fault>`)
				return
			}
		}
		dispatch(w, req)
	}
}

// streamURI is what GetStreamUri returns; the quirky profile appends a query
// string, as several vendors do.
func (c *Config) streamURI(cam Camera, s Stream) string {
	u := c.RTSPURL(cam, s)
	if c.Profile == Quirky {
		u += "?profile=" + profileToken(cam, s)
	}
	return u
}

func lookupProfile(cfg *Config, token string) (Camera, Stream, bool) {
	for _, cam := range cfg.Cameras {
		for _, s := range []Stream{Main, Sub} {
			if profileToken(cam, s) == token {
				return cam, s, true
			}
		}
	}
	return Camera{}, "", false
}

func deviceInformation(cfg *Config) string {
	return fmt.Sprintf(`<tds:GetDeviceInformationResponse>`+
		`<tds:Manufacturer>Camhive</tds:Manufacturer><tds:Model>camsim</tds:Model>`+
		`<tds:FirmwareVersion>0.1</tds:FirmwareVersion><tds:SerialNumber>CAMSIM-%d</tds:SerialNumber>`+
		`<tds:HardwareId>camsim</tds:HardwareId></tds:GetDeviceInformationResponse>`, cfg.ONVIFPort)
}

func systemDateAndTime(now time.Time) string {
	return fmt.Sprintf(`<tds:GetSystemDateAndTimeResponse><tds:SystemDateAndTime>`+
		`<tt:DateTimeType>NTP</tt:DateTimeType><tt:DaylightSavings>false</tt:DaylightSavings>`+
		`<tt:TimeZone><tt:TZ>UTC0</tt:TZ></tt:TimeZone>`+
		`<tt:UTCDateTime><tt:Time><tt:Hour>%d</tt:Hour><tt:Minute>%d</tt:Minute><tt:Second>%d</tt:Second></tt:Time>`+
		`<tt:Date><tt:Year>%d</tt:Year><tt:Month>%d</tt:Month><tt:Day>%d</tt:Day></tt:Date></tt:UTCDateTime>`+
		`</tds:SystemDateAndTime></tds:GetSystemDateAndTimeResponse>`,
		now.Hour(), now.Minute(), now.Second(), now.Year(), int(now.Month()), now.Day())
}

func capabilities(cfg *Config) string {
	media := fmt.Sprintf(`<tt:Media><tt:XAddr>%s</tt:XAddr><tt:StreamingCapabilities><tt:RTPMulticast>false</tt:RTPMulticast>`+
		`<tt:RTP_TCP>true</tt:RTP_TCP><tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP></tt:StreamingCapabilities></tt:Media>`, cfg.mediaServiceURL())
	if cfg.Profile == Quirky {
		media = "" // some cameras only advertise media through GetServices
	}
	return fmt.Sprintf(`<tds:GetCapabilitiesResponse><tds:Capabilities>`+
		`<tt:Device><tt:XAddr>%s</tt:XAddr></tt:Device>%s`+
		`<tt:PTZ><tt:XAddr>%s</tt:XAddr></tt:PTZ>`+
		`</tds:Capabilities></tds:GetCapabilitiesResponse>`, cfg.deviceServiceURL(), media, cfg.ptzServiceURL())
}

func services(cfg *Config) string {
	svc := func(ns, xaddr string, major, minor int) string {
		return fmt.Sprintf(`<tds:Service><tds:Namespace>%s</tds:Namespace><tds:XAddr>%s</tds:XAddr>`+
			`<tds:Version><tt:Major>%d</tt:Major><tt:Minor>%d</tt:Minor></tds:Version></tds:Service>`, ns, xaddr, major, minor)
	}
	return `<tds:GetServicesResponse>` +
		svc("http://www.onvif.org/ver10/device/wsdl", cfg.deviceServiceURL(), 2, 5) +
		svc("http://www.onvif.org/ver10/media/wsdl", cfg.mediaServiceURL(), 2, 5) +
		svc("http://www.onvif.org/ver20/ptz/wsdl", cfg.ptzServiceURL(), 2, 5) +
		`</tds:GetServicesResponse>`
}

func profiles(cfg *Config) string {
	var b strings.Builder
	b.WriteString(`<trt:GetProfilesResponse>`)
	for _, cam := range cfg.Cameras {
		for _, s := range []Stream{Main, Sub} {
			res := cam.resolution(s)
			fmt.Fprintf(&b, `<trt:Profiles token="%s" fixed="true"><tt:Name>%s %s</tt:Name>`+
				`<tt:VideoSourceConfiguration token="vsc_%s"><tt:Name>%s</tt:Name><tt:SourceToken>vs_%s</tt:SourceToken>`+
				`<tt:Bounds x="0" y="0" width="%d" height="%d"/></tt:VideoSourceConfiguration>`+
				`<tt:VideoEncoderConfiguration token="vec_%s_%s"><tt:Name>%s</tt:Name><tt:Encoding>%s</tt:Encoding>`+
				`<tt:Resolution><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:Resolution>`+
				`<tt:RateControl><tt:FrameRateLimit>%d</tt:FrameRateLimit></tt:RateControl></tt:VideoEncoderConfiguration></trt:Profiles>`,
				profileToken(cam, s), cam.Name, s,
				cam.Name, cam.Name, cam.Name, cam.Main.Width, cam.Main.Height,
				cam.Name, s, s, strings.ToUpper(string(cam.Codec)), res.Width, res.Height, cam.FPS)
		}
	}
	b.WriteString(`</trt:GetProfilesResponse>`)
	return b.String()
}
