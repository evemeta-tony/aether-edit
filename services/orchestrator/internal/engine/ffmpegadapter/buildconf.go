// services/orchestrator/internal/engine/ffmpegadapter/buildconf.go
//
// Startup license gate. The service must run only against an FFmpeg built to
// the AM-5 flag set (LGPL core plus NVENC via ffnvcodec headers). We assert
// that at startup by running `ffmpeg -hide_banner -buildconf` and parsing the
// output; if the build advertises --enable-gpl or --enable-nonfree the
// adapter constructor returns ErrForbiddenBuild and main logs and exits.
// This is a hard gate, not a warning.
package ffmpegadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrForbiddenBuild is returned when the ffmpeg build advertises gpl or
// nonfree configuration.
var ErrForbiddenBuild = errors.New("ffmpeg build advertises gpl or nonfree configuration; refusing to start")

// forbiddenFlags are configure flags that disqualify a build.
var forbiddenFlags = []string{"--enable-gpl", "--enable-nonfree"}

// CheckBuildconf parses `ffmpeg -buildconf` output and returns
// ErrForbiddenBuild (wrapped with the offending flag) if the build is
// disqualified. Flag matching is exact-token: --enable-gpl matches, while a
// hypothetical --enable-gplv3-thing would be a different token and is judged
// on its own text.
func CheckBuildconf(output string) error {
	for _, line := range strings.Split(output, "\n") {
		for _, tok := range strings.Fields(line) {
			tok = strings.TrimSpace(tok)
			for _, bad := range forbiddenFlags {
				if tok == bad {
					return fmt.Errorf("%w (found %s)", ErrForbiddenBuild, bad)
				}
			}
		}
	}
	return nil
}

// verifyBuild runs the buildconf gate against the configured ffmpeg binary.
func (a *Adapter) verifyBuild(ctx context.Context) error {
	stdout, stderr, err := a.runner.Output(ctx, a.cfg.FFmpegPath, []string{"-hide_banner", "-buildconf"})
	if err != nil {
		return fmt.Errorf("ffmpeg -buildconf failed (%s): %w: %s", a.cfg.FFmpegPath, err, firstLine(stderr))
	}
	// ffmpeg historically prints buildconf on stdout; some wrappers merge
	// streams, so scan both.
	if err := CheckBuildconf(string(stdout) + "\n" + string(stderr)); err != nil {
		return err
	}
	return nil
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
