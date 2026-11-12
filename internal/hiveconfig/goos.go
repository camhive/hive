package hiveconfig

import "runtime"

// runtimeGOOS is indirected so DefaultPathsFor can be tested for every OS.
var runtimeGOOS = runtime.GOOS
