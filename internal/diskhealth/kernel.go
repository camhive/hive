package diskhealth

import (
	"fmt"
	"regexp"
	"strings"
)

// Kernel is the kernel log's I/O errors on the archive's device since the
// last check.
type Kernel struct {
	Errors int
	First  string // the first such line, for the evidence
}

// kernelErrors are the kernel's words for a failed I/O on a device; the
// group is the device's kernel name (sdb, sdb1, nvme0n1, dm-2).
var kernelErrors = []*regexp.Regexp{
	regexp.MustCompile(`I/O error, dev ([^,\s]+),`), // blk_update_request: I/O error, and newer kernels without the prefix
	regexp.MustCompile(`critical medium error, dev ([^,\s]+),`),
	regexp.MustCompile(`Buffer I/O error on dev(?:ice)? ([^,\s]+),`),
	regexp.MustCompile(`EXT4-fs error \(device ([^)\s]+)\)`),
	regexp.MustCompile(`BTRFS error \(device ([^)\s]+)\)`),
	regexp.MustCompile(`XFS \(([^)\s]+)\): .*(?:I/O error|metadata I/O|corruption)`),
}

// countKernel counts the lines that report an I/O error on one of names.
func countKernel(log string, names []string) Kernel {
	var k Kernel
	for line := range strings.Lines(log) {
		if dev, ok := kernelDevice(line); ok && isOneOf(dev, names) {
			if k.Errors == 0 {
				k.First = strings.TrimSpace(line)
			}
			k.Errors++
		}
	}
	return k
}

func kernelDevice(line string) (string, bool) {
	for _, re := range kernelErrors {
		if m := re.FindStringSubmatch(line); m != nil {
			return m[1], true
		}
	}
	return "", false
}

func isOneOf(s string, list []string) bool {
	for _, l := range list {
		if s == l {
			return true
		}
	}
	return false
}

func (k Kernel) evidence() Evidence {
	return Evidence{Signal: SignalKernel, Detail: fmt.Sprintf("%d I/O errors in the kernel log, first: %s", k.Errors, k.First),
		Values: map[string]int64{"io_errors": int64(k.Errors)}}
}
