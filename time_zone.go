package ssql

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// The pipeline's zone (DFC147 §5.1, §5.7). A `time` value is an instant;
// reading its calendar — hour(ts), date_trunc(ts, "month"), strftime,
// hour(now()) — needs a zone, and the zone is chosen ONCE, where the
// stream starts, and carried in the `_schema` header: the source command
// takes `-tz ZONE`, else the TZ environment variable, else /etc/localtime
// (as date(1), Go's time.Local and DuckDB resolve it), and writes the
// name into the header. Every later command adopts the header's zone and
// never reads its own environment, so a pipeline has one zone end to
// end, `tee` keeps it, and a remote or server-side stage cannot leak its
// machine's zone in. Generated programs set it from the from stage's
// resolved name; generated SQL names it in the text it emits.
//
// This file is the one resolver and the per-process holder of that zone.

// ResolveZone resolves the pipeline zone from an explicit flag value, else
// TZ, else /etc/localtime. The name is an IANA name (`Australia/Sydney`,
// `UTC`) so it can be written into a header and into generated SQL: a TZ
// such as `EST5EDT`, or a copied (not symlinked) /etc/localtime, cannot be
// named and is an error with the remedy. No /etc/localtime at all (a bare
// container) is UTC, as Go's time.Local is.
func ResolveZone(flag string) (name string, loc *time.Location, err error) {
	switch {
	case flag != "":
		name = flag
	case os.Getenv("TZ") != "":
		name = strings.TrimPrefix(os.Getenv("TZ"), ":") // POSIX allows a leading colon
	default:
		name, err = localZoneName()
		if err != nil {
			return "", nil, err
		}
	}
	if name == "Local" || name == "" {
		return "", nil, fmt.Errorf("the time zone has no IANA name — set -tz ZONE or TZ (for example Australia/Sydney or UTC)")
	}
	loc, err = time.LoadLocation(name)
	if err != nil {
		return "", nil, fmt.Errorf("time zone %q: %v — use an IANA name such as Australia/Sydney or UTC", name, err)
	}
	return name, loc, nil
}

// localZoneName reads the system zone's IANA name from the /etc/localtime
// symlink (…/zoneinfo/Australia/Sydney). Absent: UTC. Present but not a
// symlink into a zoneinfo tree: unnamed.
func localZoneName() (string, error) {
	target, err := os.Readlink("/etc/localtime")
	if err != nil {
		if _, statErr := os.Lstat("/etc/localtime"); os.IsNotExist(statErr) {
			return "UTC", nil
		}
		return "", fmt.Errorf("/etc/localtime is not a symlink into a zoneinfo directory, so the local zone has no IANA name — set -tz ZONE or TZ")
	}
	if i := strings.LastIndex(target, "zoneinfo/"); i >= 0 {
		name := target[i+len("zoneinfo/"):]
		if strings.HasPrefix(name, "posix/") || strings.HasPrefix(name, "right/") {
			name = name[strings.Index(name, "/")+1:]
		}
		return name, nil
	}
	return "", fmt.Errorf("/etc/localtime -> %s is not in a zoneinfo directory, so the local zone has no IANA name — set -tz ZONE or TZ", target)
}

// MustLoadZone is time.LoadLocation for generated code, where the name was
// validated at generation time.
func MustLoadZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Errorf("time zone %q: %v", name, err))
	}
	return loc
}

// streamZone is the process's pipeline zone.
var streamZone struct {
	sync.Mutex
	name     string
	loc      *time.Location
	pinned   bool  // set by -tz: a header's zone does not replace it
	adopted  bool  // set from a header
	mismatch error // a second header named a different zone
}

// SetStreamZone sets the pipeline zone by IANA name (generated programs,
// the source command after resolving). The location is loaded once.
func SetStreamZone(name string) error {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return fmt.Errorf("time zone %q: %v", name, err)
	}
	streamZone.Lock()
	defer streamZone.Unlock()
	streamZone.name, streamZone.loc = name, loc
	return nil
}

// MustSetStreamZone is SetStreamZone for generated code; it returns the
// location so it can initialise a package-level var.
func MustSetStreamZone(name string) *time.Location {
	if err := SetStreamZone(name); err != nil {
		panic(err)
	}
	return streamZone.loc
}

// PinStreamZone is SetStreamZone for an explicit `-tz`: the flag wins over
// any header the command then reads.
func PinStreamZone(name string) error {
	if err := SetStreamZone(name); err != nil {
		return err
	}
	streamZone.Lock()
	streamZone.pinned = true
	streamZone.Unlock()
	return nil
}

// AdoptStreamZone takes a `_schema` header's zone as the pipeline's. The
// first header wins; a pinned -tz is kept; a later header naming a
// different zone is recorded as a mismatch for the command joining the
// two streams to refuse (StreamZoneMismatch). An empty name is no-op.
func AdoptStreamZone(name string) {
	if name == "" {
		return
	}
	streamZone.Lock()
	pinned, adopted, current := streamZone.pinned, streamZone.adopted, streamZone.name
	streamZone.Unlock()
	if pinned {
		return
	}
	if adopted {
		if name != current {
			streamZone.Lock()
			streamZone.mismatch = fmt.Errorf("the inputs name different time zones (%s and %s) — put -tz ZONE on one source so the pipeline has one zone", current, name)
			streamZone.Unlock()
		}
		return
	}
	if err := SetStreamZone(name); err != nil {
		return // an unloadable name in a header: the lazy resolution applies
	}
	streamZone.Lock()
	streamZone.adopted = true
	streamZone.Unlock()
}

// StreamZoneMismatch reports whether two input headers disagreed on the
// zone (join, union, merge check it after opening a side input).
func StreamZoneMismatch() error {
	streamZone.Lock()
	defer streamZone.Unlock()
	return streamZone.mismatch
}

// StreamZone is the pipeline zone: the one set or adopted, else resolved
// from the environment once (TZ, /etc/localtime).
func StreamZone() (string, *time.Location, error) {
	streamZone.Lock()
	defer streamZone.Unlock()
	if streamZone.loc != nil {
		return streamZone.name, streamZone.loc, nil
	}
	name, loc, err := ResolveZone("")
	if err != nil {
		return "", nil, err
	}
	streamZone.name, streamZone.loc = name, loc
	return name, loc, nil
}

// StreamZoneName is the pipeline zone's name for a header, "" when the
// environment names none (the header then carries no zone and the next
// command resolves for itself).
func StreamZoneName() string {
	name, _, err := StreamZone()
	if err != nil {
		return ""
	}
	return name
}

// StreamLocation is the pipeline zone for generated code: the location, or
// UTC with the error left to the calendar functions, which report it.
func StreamLocation() *time.Location {
	_, loc, err := StreamZone()
	if err != nil {
		return time.UTC
	}
	return loc
}

// resetStreamZone is for tests.
func resetStreamZone() {
	streamZone.Lock()
	defer streamZone.Unlock()
	streamZone.name, streamZone.loc = "", nil
	streamZone.pinned, streamZone.adopted, streamZone.mismatch = false, false, nil
}
