package commands

// ShellIntegration is one piece of ssql's optional shell wiring, each sourced
// with `eval "$(ssql FLAG)"`. This table is the SINGLE SOURCE OF TRUTH:
//
//   - `ssql -shell-init` concatenates every integration into one eval;
//   - the bare-`ssql` hint lists them (with -shell-init as the recommended
//     one-liner);
//   - main.go's intercept loop dispatches the const-script ones.
//
// So adding an integration here wires it everywhere at once. A drift test
// (TestShellIntegrationsCoverEmitters) fails if a new *KeybindingScript /
// *HelpersScript emitter isn't added to this table.
type ShellIntegration struct {
	Flag   string // e.g. "-field-keybinding"
	Desc   string // one-line summary for the bare-ssql hint
	Script string // the emitted bash; "" = generated elsewhere (autocli completion)
}

// ShellIntegrations is the ordered list. -completion-script has an empty
// Script because autocli generates it at runtime (it depends on the binary
// name); main.go fills it in for -shell-init.
var ShellIntegrations = []ShellIntegration{
	{"-completion-script", "tab completion: commands, flags, fields", ""},
	{"-field-keybinding", "Ctrl-O: pipeline-aware field and value completion", FieldKeybindingScript},
	{"-optimise-keybinding", "Ctrl-T: optimise the pipeline in place", OptimiseKeybindingScript},
	{"-help-keybinding", "Alt-h: help under cursor · Alt-H: list key bindings", HelpKeybindingScript},
	{"-code-keybinding", "Alt-g: show the typed Go the pipeline generates", CodeKeybindingScript},
	{"-run-keybinding", "Alt-r: compile the pipeline as typed Go and run it", RunKeybindingScript},
	{"-shell-helpers", "ssqlgen: turn a pipeline into Go/SQL", ShellHelpersScript},
}
