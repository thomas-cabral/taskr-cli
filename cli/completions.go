// cli/completions.go
package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// completionCommands is the first-word command list the emitted scripts
// complete. It mirrors the dispatch in Run and the usage block — three
// places one list wants to live, and none of them can derive another, so
// TestCompletionCommandsStayInSync holds this slice against the help text
// instead of trusting whoever adds the next verb to remember all three.
var completionCommands = []string{
	"context", "next", "ls", "show", "new", "group", "relate", "unrelate",
	"start", "park", "end", "close", "edit", "offload", "comment", "triage",
	"check", "step", "catchup", "timeline", "doc", "auth", "skill", "version",
	"project", "completions",
}

// refCommands are the verbs whose first positional argument is an issue
// ref. The scripts hand those positions to __complete refs; everything
// else falls through to file completion rather than guessing.
// Deliberately absent: group (wants a group, not an issue), relate and
// unrelate (want ref TYPE target — only the first position completes as a
// ref, which a one-hook script cannot express), check run (wants a check
// id), project and auth (want their own vocabularies).
var refCommands = []string{
	"show", "start", "close", "edit", "comment", "catchup", "timeline", "doc", "triage",
	"step", "check",
}

// runCompletions emits the completion script for the named shell on stdout.
// Static-first: the scripts embed the command list so tabbing works with no
// server in reach, and delegate only the issue-ref positions to
// `taskr __complete refs`, which degrades to silence — never an error or a
// hang — when the host is unreachable.
func runCompletions(args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: taskr completions [bash|zsh|fish]")
	}
	switch args[0] {
	case "bash":
		_, err := io.WriteString(stdout, bashScript())
		return err
	case "zsh":
		_, err := io.WriteString(stdout, zshScript())
		return err
	case "fish":
		_, err := io.WriteString(stdout, fishScript())
		return err
	default:
		return fmt.Errorf("usage: taskr completions [bash|zsh|fish]")
	}
}

// completeRefs prints open-and-recent issue refs, one per line, for the
// shells' dynamic hooks. Every failure mode is silence: an unreachable
// host, a missing credential or an empty org must cost the user nothing
// more than refs that do not complete — a completion that errors, hangs,
// or prints a warning into the candidate list is worse than none.
func completeRefs(ctx context.Context, c *Client, stdout io.Writer) {
	rows, err := c.ListIssues(ctx, "", []string{"open", "in_progress", "parked"}, Locator{}, false)
	if err != nil {
		return
	}
	refs := make([]string, 0, len(rows.Results))
	for _, r := range rows.Results {
		if r.Ref != "" {
			refs = append(refs, r.Ref)
		}
	}
	sort.Strings(refs)
	fmt.Fprintln(stdout, strings.Join(refs, "\n"))
}

func bashScript() string {
	var b strings.Builder
	b.WriteString("# taskr bash completion — source this or drop it in\n")
	b.WriteString("# ${BASH_COMPLETION_USER_DIR}/completions/taskr (or /etc/bash_completion.d/).\n")
	fmt.Fprintf(&b, "_taskr_cmds='%s'\n", strings.Join(completionCommands, " "))
	fmt.Fprintf(&b, "_taskr_ref_cmds='%s'\n", strings.Join(refCommands, " "))
	b.WriteString(`
_taskr_refs() {
  # Degrade to nothing: an unreachable host costs a silent tab, not an error.
  taskr __complete refs 2>/dev/null || true
}

_taskr_complete() {
  local cur prev cmds
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  cmds="${_taskr_cmds}"
  if [ "$COMP_CWORD" -eq 1 ]; then
    mapfile -t COMPREPLY < <(compgen -W "$cmds" -- "$cur")
    return 0
  fi
  local sub="${COMP_WORDS[1]}"
  case " $_taskr_ref_cmds " in
    *" $sub "*)
      if [ "$COMP_CWORD" -eq 2 ]; then
        mapfile -t COMPREPLY < <(compgen -W "$(_taskr_refs)" -- "$cur")
        return 0
      fi
      ;;
  esac
  # Flags for the known verbs, generic filenames otherwise.
  case "$prev" in
    taskr|"$sub") mapfile -t COMPREPLY < <(compgen -W "--json --help --all --untriaged" -- "$cur") ;;
  esac
}
complete -F _taskr_complete taskr
`)
	return b.String()
}

func zshScript() string {
	var b strings.Builder
	b.WriteString("#compdef taskr\n# taskr zsh completion — drop in your fpath or source it.\n")
	fmt.Fprintf(&b, "_taskr_cmds=(%s)\n", strings.Join(completionCommands, " "))
	fmt.Fprintf(&b, "_taskr_ref_cmds=(%s)\n", strings.Join(refCommands, " "))
	b.WriteString(`
_taskr_refs() {
  taskr __complete refs 2>/dev/null || true
}

_taskr() {
  local -a cmds
  cmds=(${=_taskr_cmds})
  _arguments -C \
    '1:cmd:->cmd' \
    '*::arg:->args'
  case $state in
    cmd)
      _describe 'command' cmds ;;
    args)
      case $words[1] in
        ${(j:|:)_taskr_ref_cmds})
          compadd $(_taskr_refs) ;;
        *)
          _files ;;
      esac ;;
  esac
}
_taskr "$@"
`)
	return b.String()
}

func fishScript() string {
	var b strings.Builder
	b.WriteString("# taskr fish completion — save as ~/.config/fish/completions/taskr.fish.\n")
	for _, cmd := range completionCommands {
		fmt.Fprintf(&b, "complete -c taskr -n '__fish_use_subcommand' -a '%s' -f\n", cmd)
	}
	for _, cmd := range refCommands {
		fmt.Fprintf(&b,
			"complete -c taskr -n '__fish_seen_subcommand_from %s; and test (count (commandline -opc)) -eq 2' -a '(taskr __complete refs 2>/dev/null)' -f\n",
			cmd)
	}
	b.WriteString("complete -c taskr -l json -d 'machine-readable output' -f\n")
	b.WriteString("complete -c taskr -l all -d 'widen past the resolved project' -f\n")
	b.WriteString("complete -c taskr -l untriaged -d 'rank untriaged issues too' -f\n")
	return b.String()
}
