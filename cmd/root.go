// Copyright © 2020 Harrison Brown harrybrown98@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/harrybrwn/errs"
	"github.com/harrybrwn/yt/pkg/terminal"
	"github.com/harrybrwn/yt/youtube"
	"github.com/spf13/cobra"
)

var (
	path   string // TODO: fix this!!!
	cwd, _ = os.Getwd()

	ytTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{range $i, $alias := .Aliases}}
	{{- if $i}}, {{end -}}{{$alias}}
  {{- end}}{{end}}{{if .HasAvailableSubCommands}}

Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}
{{- end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.Flags.FlagUsages | trimTrailingWhitespaces}}
{{- end -}}
{{if .HasAvailableFlags}}

Use "{{.CommandPath}} [command] --help" for more information about a command.
{{- end}}
`
)

// RootCommand returns the root command
func RootCommand() *cobra.Command {
	var rootCmd = &cobra.Command{
		Use:          "yt <command>",
		Short:        "A cli tool for downloading youtube videos.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("no Arguments\n\nUse \"yt help\" for more information")
		},
	}
	versionCmd.Flags().BoolVarP(&verboseVersion, "verbose", "v", verboseVersion, "Show all version info")
	rootCmd.PersistentFlags().StringVarP(&path, "path", "p", "", "Download path (default \"$PWD\")")
	rootCmd.SetUsageTemplate(ytTemplate)
	rootCmd.AddCommand(
		newDownloadCommand("video", "youtube videos", ".mp4"),
		newDownloadCommand("audio", "audio from youtube videos", ".mpa"),
		playlistCmd,
		newinfoCmd(true),
		testCmd,
		versionCmd,
		completionCmd,
	)
	return rootCmd
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	root := RootCommand()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

var (
	version, builtBy, commit, date string

	verboseVersion = true
	versionCmd     = &cobra.Command{
		Use:   "version",
		Short: "Show version info",
		Run: func(cmd *cobra.Command, args []string) {
			name := cmd.Root().Name()
			if version == "" {
				cmd.Printf("%s custom build\n", name)
				return
			}
			cmd.Printf("%s version %s\n", name, version)
			if !verboseVersion {
				return
			}
			cmd.Printf("built by %s", builtBy)
			if date != "" {
				cmd.Printf(" at %s", date)
			}
			cmd.Printf("\n")
			if commit != "" {
				cmd.Printf("commit: %s\n", commit)
			}
		},
	}

	completionCmd = &cobra.Command{
		Use:   "completion",
		Short: "Print a completion script to stdout.",
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			root := cmd.Root()
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				return errors.New("no shell type given")
			}
			switch args[0] {
			case "zsh":
				return root.GenZshCompletion(out)
			case "ps", "powershell":
				return root.GenPowerShellCompletion(out)
			case "bash":
				return root.GenBashCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, false)
			}
			return errs.New("unknown shell type")
		},
		ValidArgs: []string{"zsh", "bash", "ps", "powershell", "fish"},
		Aliases:   []string{"comp"},
	}
)

// SetInfo sets the version and compile info
func SetInfo(v, built, cmt, dt string) {
	version = v
	builtBy = built
	commit = cmt
	date = dt
}

type videoHandler func(v *youtube.Video) error

func newDownloadCommand(name, short, defaultExt string) *cobra.Command {
	c := &cobra.Command{
		Use:     fmt.Sprintf("%s [ids...]", name),
		Short:   fmt.Sprintf("A tool for downloading %s", short),
		Long:    fmt.Sprintf(`To download multiple videos use 'yt %s <id> <id>...'`, name),
		Aliases: []string{name[:1], name[:3]},
		RunE: func(cmd *cobra.Command, args []string) error {
			ext, err := cmd.Flags().GetString("extension")
			if err != nil {
				return err
			}
			path, err = filepath.Abs(path)
			if err != nil {
				return err
			}
			for i, arg := range args {
				if isurl(arg) {
					args[i] = getid(arg)
				}
			}

			err = handleVideos(args, func(v *youtube.Video) (err error) {
				p := filepath.Join(path, v.FileName) + ext
				switch name {
				case "audio":
					err = v.DownloadAudio(p)
				case "video":
					err = v.Download(p)
				default:
					return errors.New("bad command name")
				}
				cmd.Printf("\r%s \"%s\"\n", terminal.Green("Downloaded"), v.FileName+ext)
				return err
			})
			return err
		},
	}
	flags := c.Flags()
	flags.StringP("extension", "e", defaultExt, "File extension used for video download")
	return c
}

const loadingInterval = time.Second / 5

func handleVideos(ids []string, fn videoHandler) (err error) {
	if len(ids) == 0 {
		return errors.New("no Arguments\n\nUse \"yt [command] --help\" for more information about a command")
	}
	setCursorOnHandler()
	quit := make(chan struct{})
	terminal.CursorOff()
	defer terminal.CursorOn()

	if len(ids) > 1 {
		go func() {
			err = asyncDownload(ids, fn)
			close(quit)
		}()
	} else if len(ids) == 1 {
		go func() {
			var v *youtube.Video
			defer close(quit)
			v, err = youtube.NewVideo(ids[0])
			if err != nil {
				print("\r")
				return
			}
			err = fn(v)
		}()
	}
	for i := 0; ; i++ {
		select {
		case <-quit:
			return err
		default:
			fmt.Printf("\r%s...  %c", terminal.Red("Downloading"), getLoadingChar(i))
			time.Sleep(loadingInterval)
		}
	}
}

func newinfoCmd(hidden bool) *cobra.Command {
	type infocommand struct {
		fflags, playerResp bool
	}
	ic := infocommand{false, false}

	infoCmd := &cobra.Command{
		Use:   "info",
		Short: "Get extra information for a youtube video",
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := youtube.GetInfo(args[0])
			if err != nil {
				return err
			}
			if ic.fflags {
				return printfflags(info)
			}
			if ic.playerResp {
				fmt.Printf("%s\n", info["player_response"])
				return nil
			}
			for k, v := range info {
				if k == "player_response" || k == "fflags" {
					continue
				}
				fmt.Printf("%s: %s\n", k, v[0])
			}
			return nil
		},
		Hidden: hidden,
	}
	infoCmd.Flags().BoolVar(&ic.fflags, "fflags", ic.fflags, "Print out the fflags")
	infoCmd.Flags().BoolVar(&ic.playerResp, "player-response", ic.playerResp, "Print out the raw player response data")
	return infoCmd
}

func getLoadingChar(i int) rune {
	switch i % 4 {
	case 0:
		return '|'
	case 1:
		return '/'
	case 2:
		return '-'
	case 3:
		return '\\'
	default:
		panic("modulus is broken")
	}
}

func printLoadingChar(i int) {
	fmt.Printf("\b%c", getLoadingChar(i))
}

func printfflags(info map[string][][]byte) error {
	f, ok := info["fflags"]
	if !ok || len(f) == 0 {
		return errors.New("could not find fflags")
	}
	data := string(f[0])

	res, err := url.ParseQuery(data)
	if err != nil {
		return err
	}
	for k, v := range res {
		fmt.Println(k, v)
	}
	return nil
}

func asyncDownload(ids []string, fn videoHandler) (err error) {
	var wg sync.WaitGroup
	wg.Add(len(ids))
	for _, id := range ids {
		go func(id string) {
			defer wg.Done()
			if isurl(id) {
				id = getid(id)
			}
			v, err := youtube.NewVideo(id)
			if err != nil {
				log.Println(err)
				return
			}
			if e := fn(v); e != nil {
				log.Println(e)
				if err == nil {
					err = e
				}
			}
		}(id)
	}
	wg.Wait()
	return err
}

var testCmd = &cobra.Command{
	Use:    "test",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}
