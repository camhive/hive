package datamove

import "io"

// FailReading makes reading the files that fail says yes to fail, as a
// bad sector does; restore puts the real reader back.
func FailReading(fail func(path string) bool) (restore func()) {
	real := openSource
	openSource = func(path string) (io.ReadCloser, error) {
		if fail(path) {
			return failing{}, nil
		}
		return real(path)
	}
	return func() { openSource = real }
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, errIO }
func (failing) Close() error             { return nil }

var errIO = &ioError{}

type ioError struct{}

func (*ioError) Error() string { return "input/output error" }
