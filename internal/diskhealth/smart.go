package diskhealth

import (
	"encoding/json"
	"strings"
)

// SMART is what smartctl -j -H -A said about one disk.
type SMART struct {
	Device       string
	Passed       bool
	Reallocated  int64 // ATA 5
	Reported     int64 // ATA 187, reported uncorrectable
	Pending      int64 // ATA 197
	Offline      int64 // ATA 198, offline uncorrectable
	NVMe         bool
	Used         int64 // NVMe percentage_used
	MediaErrors  int64 // NVMe
	Warning      int64 // NVMe critical_warning
	TemperatureC *int
}

// smartJSON is the part of smartctl's JSON we read.
type smartJSON struct {
	Smartctl struct {
		Messages []struct {
			String string `json:"string"`
		} `json:"messages"`
	} `json:"smartctl"`
	Device struct {
		Name string `json:"name"`
	} `json:"device"`
	Status *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	ATA *struct {
		Table []struct {
			ID  int `json:"id"`
			Raw struct {
				Value int64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	NVMe *struct {
		Warning     int64 `json:"critical_warning"`
		Used        int64 `json:"percentage_used"`
		MediaErrors int64 `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
	Temperature *struct {
		Current int `json:"current"`
	} `json:"temperature"`
}

// parseSMART reads smartctl's JSON. ok is false when smartctl could not
// read SMART (no smart_status): permission, an unsupported device, or a
// USB bridge it needs told about (needsType).
func parseSMART(raw []byte) (s SMART, ok, needsType bool) {
	var j smartJSON
	if json.Unmarshal(raw, &j) != nil {
		return SMART{}, false, false
	}
	for _, m := range j.Smartctl.Messages {
		if strings.Contains(m.String, "Unknown USB bridge") || strings.Contains(m.String, "-d option") {
			needsType = true
		}
	}
	if j.Status == nil {
		return SMART{}, false, needsType
	}
	s = SMART{Device: j.Device.Name, Passed: j.Status.Passed}
	if j.ATA != nil {
		attr := map[int]*int64{5: &s.Reallocated, 187: &s.Reported, 197: &s.Pending, 198: &s.Offline}
		for _, a := range j.ATA.Table {
			if f, ok := attr[a.ID]; ok {
				*f = a.Raw.Value
			}
		}
	}
	if n := j.NVMe; n != nil {
		s.NVMe, s.Used, s.MediaErrors, s.Warning = true, n.Used, n.MediaErrors, n.Warning
	}
	if j.Temperature != nil {
		t := j.Temperature.Current
		s.TemperatureC = &t
	}
	return s, true, needsType
}

// evidence is the SMART reasons for failing; none for a healthy disk.
func (s SMART) evidence() []Evidence {
	v := map[string]int64{}
	var why []string
	if !s.Passed {
		v["passed"] = 0
		why = append(why, "SMART overall health check failed")
	}
	if s.NVMe {
		why = append(why, s.nvmeReasons(v)...)
	} else {
		why = append(why, s.ataReasons(v)...)
	}
	if len(why) == 0 {
		return nil
	}
	return []Evidence{{Signal: SignalSMART, Detail: s.Device + ": " + strings.Join(why, ", "), Values: v}}
}

// ataReasons are the sector counts that count; v gets all of them when any does.
func (s SMART) ataReasons(v map[string]int64) []string {
	var why []string
	if s.Reallocated >= ReallocatedLimit {
		why = append(why, "reallocated sectors")
	}
	if s.Pending > 0 {
		why = append(why, "pending sectors")
	}
	if s.Offline > 0 || s.Reported > 0 {
		why = append(why, "uncorrectable sectors")
	}
	if len(why) > 0 || !s.Passed {
		v["reallocated_sectors"], v["pending_sectors"], v["offline_uncorrectable"], v["reported_uncorrectable"] = s.Reallocated, s.Pending, s.Offline, s.Reported
	}
	return why
}

// nvmeTemperature is the critical_warning bit for a temperature out of
// range: heat is shown, never evidence of failing.
const nvmeTemperature = 0x02

// nvmeReasons are the wear and error counts that count.
func (s SMART) nvmeReasons(v map[string]int64) []string {
	var why []string
	if s.Used >= WornPercent {
		v["percentage_used"] = s.Used
		why = append(why, "rated endurance used up")
	}
	if s.MediaErrors > 0 || s.Warning&^nvmeTemperature != 0 {
		v["media_errors"], v["critical_warning"] = s.MediaErrors, s.Warning
		why = append(why, "NVMe media errors or critical warning")
	}
	return why
}
