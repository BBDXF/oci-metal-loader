package tui

// Strings holds every user-visible UI string (English). Translating the TUI later means providing another Strings value;
// no other file contains UI text (key names and data values excepted).
type Strings struct {
	Title, Tabs                                                   []string
	AppName                                                       string
	HintImages, HintDetail, HintPull, HintStorage, HintNet        string
	HintSettings, HintOp, HintDialog, HintForm, HintHelp          string
	NoImages, NoSnaps, Loading, Refreshed, Cancelled, Running     string
	Done, Failed, Close, Yes, No, Cancel, Warning, Error, Info    string
	ColName, ColType, ColSize, ColBase, ColSnaps, ColCurrent      string
	ColUsed                                                       string
	BaseNode, CurrentMark, Frozen, Leaf, Ephemeral, NoLayer       string
	QuitTitle, QuitText                                           string
	PowerTitle, PowerReboot, PowerOff, PowerRebooting, PowerBusy  string
	BootTitle, BootCurrent, BootSelected, BootThrowaway, BootBase string
	BootConfirm, BootPreview, BootStarting                        string
	BootContinues, BootEphemeralNote                              string
	CurrentNode, EphemeralNode                                    string
	CreateTitle, FieldName, FieldNote, FieldTag, CreateHelp       string
	RevertTitle, RevertKeep, RevertDiscard, RevertSure            string
	RevertSelect, RevertKeepHelp                                  string
	DeleteTitle, DeleteRec, DeleteCurrent, DeleteBase             string
	DeleteNotSnap, DeleteMerge, TagNotSnap, SelectSnap            string
	TagTitle, TagHelp, NoteTitle                                  string
	PruneTitle, PruneRetention, PruneGC, PruneNothing, PruneAsk   string
	FlattenTitle, ExportTitle, FieldOut, VerifyTitle, VerifyDeep  string
	VerifyOK                                                      string
	SourcesTitle, SourceTest, SourceBlocked, SourceOffline        string
	CatalogTitle, FilterTitle, FieldOS, FieldRelease, FieldType   string
	PullTitle, PullConfirm, PullName, PullProgress, NoSources     string
	PullOffline, NoCatalog                                        string
	StoreTitle, StoreNone, StoreCreate, StoreGrow, StoreSize      string
	StorePath, StoreWizard, StoreGuard, StoreGuardOK              string
	StoreGuardLow, StoreDirty, StoreClean                         string
	NetTitle, SettingsTitle                                       string
	HelpTitle                                                     string
	HelpLines                                                     []string
	ASCIINote                                                     string
	TooSmall                                                      string
}

// L is the active string table.
var L = English()

// English returns the default (English) strings.
func English() Strings {
	return Strings{
		AppName: "OCI Metal Loader",
		Tabs:    []string{"Images", "Pull", "Storage", "Network", "Settings"},

		HintImages:   "Enter:open  r:refresh  1-5:tabs  s:shell  P:power  ?:help  q:quit",
		HintDetail:   "Enter/b:boot c:create r:revert d:del t:tag n:note p:prune f:flat e:exp v:ver",
		HintPull:     "Enter:select  t:test source  /:filter  r:reload  Esc:back  ?:help",
		HintStorage:  "c:create/grow store file  r:refresh  1-5:tabs  ?:help",
		HintNet:      "r:refresh  t:test sources  1-5:tabs  ?:help",
		HintSettings: "Up/Down:scroll  1-5:tabs  ?:help",
		HintOp:       "Ctrl+C:cancel  Up/Down:scroll  Enter/Esc:close when finished",
		HintDialog:   "Enter:ok  Esc:cancel",
		HintForm:     "Tab/Down:next field  Enter:ok  Esc:cancel",
		HintHelp:     "any key: close",

		NoImages: "No images found. Use the Pull tab (2) to download one, or copy images to the images partition.",
		NoSnaps:  "No snapshots yet: base (original image) plus your live state.",
		Loading:  "Loading...", Refreshed: "Refreshed.", Cancelled: "Cancelled.", Running: "running",
		Done: "finished", Failed: "FAILED", Close: "close", Yes: "Yes", No: "No", Cancel: "Cancel",
		Warning: "WARNING", Error: "ERROR", Info: "Info",

		ColName: "NAME", ColType: "TYPE", ColSize: "SIZE", ColBase: "BASE", ColSnaps: "SNAPS", ColCurrent: "LIVE ON", ColUsed: "LAST USED",
		BaseNode: "base", CurrentMark: "*", Frozen: "frozen", Leaf: "leaf", Ephemeral: "ephemeral", NoLayer: "NO-LAYER",

		QuitTitle: "Leave the TUI", QuitText: "Leave the TUI and open the shell? (Exiting the shell shows the TUI again.)",
		PowerTitle: "Power", PowerReboot: "Reboot", PowerOff: "Power off", PowerRebooting: "Shutting down...", PowerBusy: "an operation is still running",

		BootTitle: "Boot", BootCurrent: "Continue current (your live state)", BootSelected: "selected snapshot, read-only (ephemeral)",
		BootThrowaway: "current, throwaway (ephemeral: changes are discarded)", BootBase: "base (original image), ephemeral (changes are discarded)",
		BootConfirm: "Boot now?", BootPreview: "d: preview (dry-run)",
		BootContinues:     "Continues the live state: changes are kept in the live layer (no new layer per boot).",
		BootEphemeralNote: "Ephemeral boot: runs on a throwaway layer that is discarded before the next boot/snapshot operation.",
		BootStarting:      "Handing over to the booted system...",
		CurrentNode:       "current (you are here)", EphemeralNode: "(ephemeral boot)",

		CreateTitle: "Create snapshot", FieldName: "Name", FieldNote: "Note", FieldTag: "Tag",
		CreateHelp:  "Freezes the live state as a snapshot; the next boot continues in a fresh layer on top.",
		RevertTitle: "Revert", RevertDiscard: "Revert, discard the current live state", RevertKeep: "Revert, keep the current live state as a snapshot...",
		RevertSure:     "The changes of the current live state are discarded permanently.",
		RevertSelect:   "Select a snapshot or the base image to revert to.",
		RevertKeepHelp: "Name of the snapshot that will keep the current live state:",
		DeleteTitle:    "Delete snapshot", DeleteRec: "This deletes the snapshot and everything below it (recursive).", DeleteCurrent: "The live state is inside; delete it too (--force)?",
		DeleteBase:    "The base image is the read-only original and cannot be deleted.",
		DeleteNotSnap: "Select a snapshot. (The live state is discarded with Revert.)",
		DeleteMerge:   "Its changes are merged into the child snapshot below it (overlay images only).",
		TagNotSnap:    "Tags and notes apply to snapshots.", SelectSnap: "Select a snapshot.",
		TagTitle: "Tag", TagHelp: "Tag to add (prefix with - to remove)", NoteTitle: "Note",
		PruneTitle: "Prune / GC", PruneRetention: "Prune by retention (keep_last / keep_tagged)", PruneGC: "GC: remove leftovers of ephemeral boots",
		PruneNothing: "Nothing to delete.", PruneAsk: "Delete these snapshots?",
		FlattenTitle: "Flatten", ExportTitle: "Export", FieldOut: "Output file", VerifyTitle: "Verify", VerifyDeep: "Deep verify (hash the base image, slow)?", VerifyOK: "verify: OK",

		SourcesTitle: "Sources", SourceTest: "Testing sources", SourceBlocked: "blocked by policy", SourceOffline: "offline only",
		CatalogTitle: "Catalog", FilterTitle: "Filter", FieldOS: "OS", FieldRelease: "Release", FieldType: "Type (container|vm; LXD image type)",
		PullTitle: "Pull image", PullConfirm: "Download and import this image?", PullName: "Image name", PullProgress: "downloaded", NoSources: "No sources configured.",
		PullOffline: "Online access is disabled by policy or offline mode; only cached images can be used.", NoCatalog: "No catalog loaded. Press Enter on a source.",

		StoreTitle: "Storage", StoreNone: "no store file configured/found", StoreCreate: "create", StoreGrow: "grow", StoreSize: "Size (e.g. 20G)",
		StorePath: "Store file path", StoreWizard: "Store file wizard", StoreGuard: "Space guard", StoreGuardOK: "ok", StoreGuardLow: "BELOW min_free_percent: create/boot are refused",
		StoreDirty: "NOT clean (run `omloader store check`)", StoreClean: "clean",

		NetTitle: "Network", SettingsTitle: "Settings (read-only, resolved config with source layer)",
		HelpTitle: "Help",
		HelpLines: []string{
			"Navigation: Up/Down or j/k, PgUp/PgDn, Home/End, Enter = select, Esc = back.",
			"Tabs: 1 Images  2 Pull  3 Storage  4 Network  5 Settings  (Tab/Shift+Tab cycle).",
			"s = shell (exit the shell to come back), P = reboot / power off, q or Esc on",
			"the first screen = leave the TUI.  Ctrl+C cancels a running operation; it",
			"never quits the TUI.  Ctrl+L redraws.  Long operations stream their output.",
			"Boot hands over to the booted system; nothing in the TUI runs afterwards.",
		},
		ASCIINote: "ASCII",
		TooSmall:  "Terminal too small (need 80x24)",
	}
}
