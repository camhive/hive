package camera

import "fmt"

// Sensitivity is how readily the Hive's motion detection reacts on a
// camera (#157). The zero value is normal, ADR-0020's defaults; low and
// high move the threshold and the minimum episode length (internal/motion
// maps them).
type Sensitivity int8

// Sensitivities; normal is the zero value.
const (
	SensitivityNormal Sensitivity = iota
	SensitivityLow
	SensitivityHigh
)

var sensitivityNames = map[Sensitivity]string{SensitivityNormal: "normal", SensitivityLow: "low", SensitivityHigh: "high"}

// String is the name in the API and the store.
func (s Sensitivity) String() string {
	if n, ok := sensitivityNames[s]; ok {
		return n
	}
	return fmt.Sprintf("Sensitivity(%d)", int8(s))
}

// ParseSensitivity reads low, normal or high.
func ParseSensitivity(name string) (Sensitivity, error) {
	for s, n := range sensitivityNames {
		if n == name {
			return s, nil
		}
	}
	return 0, fmt.Errorf("%w: sensitivity is low, normal or high", ErrInvalid)
}

func (s Sensitivity) validate() error {
	if _, ok := sensitivityNames[s]; !ok {
		return fmt.Errorf("%w: unknown sensitivity %d", ErrInvalid, int8(s))
	}
	return nil
}
