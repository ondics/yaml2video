package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ondics/yaml2video/src/render"
)

type cliOptions struct {
	videoPath    string
	templatePath string
	render       render.Options
	dryRun       bool
	help         bool
	serve        bool
	fonts        bool
	offlineOnly  bool
}

func newFlagSet(options *cliOptions) *flag.FlagSet {
	flags := flag.NewFlagSet("yaml2video", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	flags.BoolVar(&options.help, "h", false, "show this help page")
	flags.BoolVar(&options.help, "help", false, "show this help page")
	flags.BoolVar(&options.dryRun, "n", false, "validate and preview the render plan and FFmpeg commands without rendering")
	flags.StringVar(&options.render.WorkDir, "work-dir", "", "directory for intermediate render files and base for relative template_file paths (default intermediates: .yaml2video beside the video YAML)")
	flags.StringVar(&options.render.Output, "o", "", "output file (default: output.mp4 beside the video YAML)")
	flags.StringVar(&options.templatePath, "t", "", "template YAML file (overrides template_file in the video YAML)")
	return flags
}

func parseArgs() (cliOptions, error) {
	args := os.Args[1:]
	var options cliOptions
	if len(args) == 0 {
		return options, errors.New("a video YAML file, serve, or fonts command is required")
	}

	if args[0] == "fonts" {
		flags := flag.NewFlagSet("fonts", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		flags.BoolVar(&options.help, "h", false, "show help")
		flags.BoolVar(&options.help, "help", false, "show help")
		flags.BoolVar(&options.offlineOnly, "offline-only", false, "only list families available offline")
		if err := flags.Parse(args[1:]); err != nil {
			return options, err
		}
		if options.help {
			return options, flag.ErrHelp
		}
		if flags.NArg() != 0 {
			return options, errors.New("fonts does not accept positional arguments")
		}
		options.fonts = true
		return options, nil
	}

	flags := newFlagSet(&options)
	if args[0] == "serve" {
		if err := flags.Parse(args[1:]); err != nil {
			return options, err
		}
		if options.help {
			return options, flag.ErrHelp
		}
		if len(args) != 1 {
			return options, fmt.Errorf("%s does not accept additional arguments", args[0])
		}
		options.serve = args[0] == "serve"

		return options, nil
	}

	// Go's flag parser stops at the first positional argument. Accept the
	// video path before the flags as well as after them.
	flagArgs := args
	if !strings.HasPrefix(args[0], "-") {
		options.videoPath = args[0]
		flagArgs = args[1:]
	}
	if err := flags.Parse(flagArgs); err != nil {
		return options, err
	}
	if options.help {
		return options, flag.ErrHelp
	}

	if options.videoPath == "" {
		if flags.NArg() != 1 {
			return options, errors.New("exactly one video YAML file is required")
		}
		options.videoPath = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return options, errors.New("exactly one video YAML file is required; put flags before or after the video path")
	}

	return options, nil
}
