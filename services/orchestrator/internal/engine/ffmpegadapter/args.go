// services/orchestrator/internal/engine/ffmpegadapter/args.go
//
// Argv construction for ffmpeg encodes. Everything is an argv array through
// os/exec (S3); there is no shell anywhere. The only externally influenced
// argv elements are filesystem paths that the service itself derives from a
// validated objectKey (strict regexp at the service boundary) and from its
// own staging directories. Progress goes out over a pipe, not argv.
package ffmpegadapter

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/engine"
)

// encoderFor maps a codec-neutral name to the AM-5 NVENC encoder.
func encoderFor(codec string) (string, error) {
	switch codec {
	case "h264":
		return "h264_nvenc", nil
	case "hevc":
		return "hevc_nvenc", nil
	case "av1":
		return "av1_nvenc", nil
	}
	return "", fmt.Errorf("unsupported video codec %q", codec)
}

// audioArgsFor returns the audio codec argv for a container.
func audioArgsFor(container string) []string {
	if container == "webm" {
		// webm cannot carry aac; opus is the AM-5 audio codec for webm.
		return []string{"-c:a", "libopus", "-b:a", "128k"}
	}
	return []string{"-c:a", "aac", "-b:a", "192k"}
}

// buildTranscodeArgs builds the full ffmpeg argv (excluding the binary name)
// for one output rung. It returns the argv and the primary output artifact
// path (the muxed file, or the playlist / manifest for hls and dash).
func buildTranscodeArgs(inputPath string, out engine.OutputSpec) ([]string, string, error) {
	enc, err := encoderFor(out.VideoCodec)
	if err != nil {
		return nil, "", err
	}

	args := []string{
		"-hide_banner",
		"-nostdin",
		"-nostats",
		"-y",
		"-progress", "pipe:1",
		"-i", inputPath,
		"-map", "0:v:0",
	}
	if out.IncludeAudio {
		args = append(args, "-map", "0:a:0")
	}
	args = append(args,
		"-vf", fmt.Sprintf("scale=%d:%d", out.Width, out.Height),
		"-c:v", enc,
		"-preset", out.SpeedPreset,
		"-g", strconv.Itoa(out.GOPLength),
	)

	switch out.RateControl.Mode {
	case "crf":
		// NVENC constant quality: vbr rate control with -cq and no bitrate
		// target.
		args = append(args, "-rc", "vbr", "-cq", strconv.Itoa(out.RateControl.CRF), "-b:v", "0")
	case "vbr":
		args = append(args, "-rc", "vbr", "-b:v", fmt.Sprintf("%dk", out.RateControl.BitrateKbps))
		if out.RateControl.MaxBitrateKbps > 0 {
			args = append(args, "-maxrate", fmt.Sprintf("%dk", out.RateControl.MaxBitrateKbps))
		}
	case "cbr":
		kb := fmt.Sprintf("%dk", out.RateControl.BitrateKbps)
		args = append(args, "-rc", "cbr", "-b:v", kb, "-minrate", kb, "-maxrate", kb)
	default:
		return nil, "", fmt.Errorf("unsupported rate control mode %q", out.RateControl.Mode)
	}

	if out.IncludeAudio {
		args = append(args, audioArgsFor(out.Container)...)
	} else {
		args = append(args, "-an")
	}

	var artifact string
	switch out.Container {
	case "mp4":
		artifact = filepath.Join(out.DestDir, out.RungName+".mp4")
		args = append(args, "-movflags", "+faststart", "-f", "mp4", artifact)
	case "mov":
		artifact = filepath.Join(out.DestDir, out.RungName+".mov")
		args = append(args, "-f", "mov", artifact)
	case "webm":
		artifact = filepath.Join(out.DestDir, out.RungName+".webm")
		args = append(args, "-f", "webm", artifact)
	case "hls":
		artifact = filepath.Join(out.DestDir, out.RungName+".m3u8")
		args = append(args,
			"-f", "hls",
			"-hls_time", "6",
			"-hls_playlist_type", "vod",
			"-hls_segment_type", "fmp4",
			"-hls_segment_filename", filepath.Join(out.DestDir, out.RungName+"_%05d.m4s"),
			artifact,
		)
	case "dash":
		artifact = filepath.Join(out.DestDir, out.RungName+".mpd")
		args = append(args,
			"-f", "dash",
			"-seg_duration", "6",
			"-init_seg_name", out.RungName+"_init_$RepresentationID$.m4s",
			"-media_seg_name", out.RungName+"_chunk_$RepresentationID$_$Number%05d$.m4s",
			artifact,
		)
	default:
		return nil, "", fmt.Errorf("unsupported container %q", out.Container)
	}
	return args, artifact, nil
}
