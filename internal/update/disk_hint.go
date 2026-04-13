package update

import (
	"bytes"
	"fmt"
	"io"
)

// diskHintSentinel is the substring emitted by podman (and the OS) when a
// build step runs out of space on the graphroot volume. Matching on the
// human-readable form keeps the detector runtime-agnostic: both the kernel
// error and node.js' copyfile wrapper surface the same phrase.
var diskHintSentinel = []byte("no space left on device")

// diskHintWriter is an io.Writer that forwards every byte to the underlying
// writer while scanning for diskHintSentinel. Once the sentinel appears in
// the stream, Tripped returns true. The scan carries a small tail buffer so
// the sentinel is still detected when it straddles a Write boundary.
type diskHintWriter struct {
	w       io.Writer
	tail    []byte
	tripped bool
}

func newDiskHintWriter(w io.Writer) *diskHintWriter {
	return &diskHintWriter{w: w}
}

func (d *diskHintWriter) Write(p []byte) (int, error) {
	n, err := d.w.Write(p)
	if !d.tripped {
		scan := make([]byte, 0, len(d.tail)+n)
		scan = append(scan, d.tail...)
		scan = append(scan, p[:n]...)
		if bytes.Contains(bytes.ToLower(scan), diskHintSentinel) {
			d.tripped = true
			d.tail = nil
		} else {
			// Retain only the last len(sentinel)-1 bytes so a match that
			// spans the next Write is still catchable.
			keep := len(diskHintSentinel) - 1
			if len(scan) > keep {
				scan = scan[len(scan)-keep:]
			}
			d.tail = append(d.tail[:0], scan...)
		}
	}
	return n, err
}

// Tripped reports whether the sentinel has been observed.
func (d *diskHintWriter) Tripped() bool { return d.tripped }

// emitDiskHint writes a reclaim hint to stderr. Callers invoke it only
// after a build failure whose stderr stream tripped the detector. The
// commands are printed but not executed: reclaiming image storage is
// destructive and the user decides when to run it.
func emitDiskHint(stderr io.Writer) {
	fmt.Fprintln(stderr, "confine-ai update: hint: build failed with 'no space left on device'.")
	fmt.Fprintln(stderr, "  Reclaim podman storage by running these yourself:")
	fmt.Fprintln(stderr, "    podman image prune -af")
	fmt.Fprintln(stderr, "    podman system prune -f")
}
