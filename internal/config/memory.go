package config

import (
	"io/fs"
	"math"
	"os"
	"path"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
)

// noCgroupLimit is where cgroup v1 values stop being a limit: it reports
// MaxInt64 rounded down to the page size when none is set.
const noCgroupLimit = 1 << 62

// cgroupFields is the field count of a proc/self/cgroup line: the
// hierarchy ID, the controllers and the cgroup's path.
const cgroupFields = 3

// FromGOMEMLIMIT is the source [MemoryLimit] names when GOMEMLIMIT set the
// limit.
const FromGOMEMLIMIT = "GOMEMLIMIT"

// MemoryLimit returns the process's memory limit in bytes, 0 for none, and
// where it came from: [FromGOMEMLIMIT], otherwise the file of the smallest
// memory limit set on the process's cgroup or any cgroup above it.
func MemoryLimit() (limit int64, source string) {
	if runtimeLimit := debug.SetMemoryLimit(-1); runtimeLimit != math.MaxInt64 {
		return runtimeLimit, FromGOMEMLIMIT
	}

	if cgroupLimit, src, ok := cgroupMemoryLimit(os.DirFS("/")); ok {
		return cgroupLimit, src
	}

	return 0, "none"
}

// cgroupMemoryLimit finds the process's cgroup in proc/self/cgroup of fsys,
// a file system rooted at /, and returns the smallest memory limit on the
// way from that cgroup up to the root, and the file it came from.
func cgroupMemoryLimit(fsys fs.FS) (int64, string, bool) {
	b, err := fs.ReadFile(fsys, "proc/self/cgroup")
	if err != nil {
		return 0, "", false
	}

	for line := range strings.Lines(string(b)) {
		fields := strings.SplitN(strings.TrimSpace(line), ":", cgroupFields)
		if len(fields) != cgroupFields {
			continue
		}

		var dir, file string

		switch {
		case fields[0] == "0" && fields[1] == "":
			dir, file = "sys/fs/cgroup", "memory.max"
		case slices.Contains(strings.Split(fields[1], ","), "memory"):
			dir, file = "sys/fs/cgroup/memory", "memory.limit_in_bytes"
		default:
			continue
		}

		if limit, src, ok := smallestLimit(fsys, dir, fields[2], file); ok {
			return limit, src, true
		}
	}

	return 0, "", false
}

// smallestLimit reads file in the cgroup directory for group under dir and
// in every parent up to dir itself, and returns the smallest limit set.
func smallestLimit(fsys fs.FS, dir, group, file string) (int64, string, bool) {
	var (
		best int64
		src  string
	)

	for cgroup := path.Clean("/" + group); ; cgroup = path.Dir(cgroup) {
		name := path.Join(dir, cgroup, file)

		if b, err := fs.ReadFile(fsys, name); err == nil {
			if limit, ok := parseCgroupLimit(string(b)); ok && (best == 0 || limit < best) {
				best, src = limit, "/"+name
			}
		}

		if cgroup == "/" {
			return best, src, best > 0
		}
	}
}

// parseCgroupLimit reads a cgroup memory limit file. "max", and the near
// maximum value cgroup v1 reports for no limit, mean none.
func parseCgroupLimit(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 || n >= noCgroupLimit {
		return 0, false
	}

	return n, true
}
