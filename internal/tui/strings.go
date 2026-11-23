package tui

// Strings holds every user-visible UI string (English). Translating the TUI later means providing another Strings value;
// no other file contains UI text (key names and data values excepted).
type Strings struct {
	Title, Tabs                                                  []string
	AppName                                                      string
	HintImages, HintDetail, HintPull, HintStorage, HintNet       string
	HintSettings, HintOp, HintDialog, HintForm, HintHelp         string
	NoImages, NoSnaps, Loading, Refreshed, Cancelled, Running    string
	Done, Failed, Close, Yes, No, Cancel, Warning, Error, Info   string
	ColName, ColType, ColSize, ColBase, ColSnaps, ColCurrent     string
	ColUsed                                                      string
	BaseNode, CurrentMark, Frozen, Leaf, Ephemeral, NoLayer      string
	QuitTitle, QuitText                                          string
	PowerTitle, PowerReboot, PowerOff, PowerRebooting, PowerBusy string
	BootTitle, BootCurrent, BootSelected, BootNew, BootBase      string
	BootNewPrompt, BootConfirm, BootPreview, BootForceTitle      string
	BootForceText, BootStarting                                  string
	CreateTitle, FieldName, FieldNote, FieldTag, FieldFrom       string
	RollbackTitle, RollbackKeep, RollbackDiscard, RollbackSure   string
	DeleteTitle, DeleteRec, DeleteCurrent                        string
	TagTitle, TagHelp, NoteTitle                                 string
	PruneTitle, PruneRetention, PruneGC, PruneNothing, PruneAsk  string
	FlattenTitle, ExportTitle, FieldOut, VerifyTitle, VerifyDeep string
	VerifyOK                                                     string
	SourcesTitle, SourceTest, SourceBlocked, SourceOffline       string
	CatalogTitle, FilterTitle, FieldOS, FieldRelease, FieldType  string
	PullTitle, PullConfirm, PullName, PullProgress, NoSources    string
	PullOffline, NoCatalog                                       string
	StoreTitle, StoreNone, StoreCreate, StoreGrow, StoreSize     string
	StorePath, StoreWizard, StoreGuard, StoreGuardOK             string
	StoreGuardLow, StoreDirty, StoreClean                        string
	NetTitle, SettingsTitle                                      string
	HelpTitle                                                    string
	HelpLines                                                    []string
	ASCIINote                                                    string
	TooSmall                                                     string
}

// L is the active string table.
var L = English()

// English returns the default (English) strings.
func English() Strings {
	return Strings{
		AppName: "OCI Metal Loader",
		Tabs:    []string{"Images", "Pull", "Storage", "Network", "Settings"},

		HintImages:   "Enter:open  r:refresh  1-5:tabs  s:shell  P:power  ?:help  q:quit",
		HintDetail:   "Enter/b:boot c:create r:rollback d:delete t:tag n:note p:prune f:flatten e:export v:verify Esc:back",
		HintPull:     "Enter:select  t:test source  /:filter  r:reload  Esc:back  ?:help",
		HintStorage:  "c:create/grow store file  r:refresh  1-5:tabs  ?:help",
		HintNet:      "r:refresh  t:test sources  1-5:tabs  ?:help",
		HintSettings: "Up/Down:scroll  1-5:tabs  ?:help",
		HintOp:       "Ctrl+C:cancel  Up/Down:scroll  Enter/Esc:close when finished",
		HintDialog:   "Enter:ok  Esc:cancel",
		HintForm:     "Tab/Down:next field  Enter:ok  Esc:cancel",
		HintHelp:     "any key: close",

		NoImages: "No images found. Use the Pull tab (2) to download one, or copy images to the images partition.",
		NoSnaps:  "No snapshots yet (only the base image).",
		Loading:  "Loading...", Refreshed: "Refreshed.", Cancelled: "Cancelled.", Running: "running",
		Done: "finished", Failed: "FAILED", Close: "close", Yes: "Yes", No: "No", Cancel: "Cancel",
		Warning: "WARNING", Error: "ERROR", Info: "Info",

		ColName: "NAME", ColType: "TYPE", ColSize: "SIZE", ColBase: "BASE", ColSnaps: "SNAPS", ColCurrent: "CURRENT", ColUsed: "LAST USED",
		BaseNode: "base", CurrentMark: "*", Frozen: "frozen", Leaf: "leaf", Ephemeral: "ephemeral", NoLayer: "NO-LAYER",

		QuitTitle: "Leave the TUI", QuitText: "Leave the TUI and open the shell? (Exiting the shell shows the TUI again.)",
		PowerTitle: "Power", PowerReboot: "Reboot", PowerOff: "Power off", PowerRebooting: "Shutting down...", PowerBusy: "an operation is still running",

		BootTitle: "Boot", BootCurrent: "current snapshot", BootSelected: "selected snapshot", BootNew: "new snapshot (named)...",
		BootBase: "base image, ephemeral (changes are discarded)", BootNewPrompt: "Name of the new snapshot",
		BootConfirm: "Boot now?", BootPreview: "d: preview (dry-run)", BootForceTitle: "Booting a snapshot that has children",
		BootForceText: "This snapshot is frozen (it has child snapshots). Booting it needs --force, writes into a layer its descendants depend on and can corrupt them. Continue only if you know what you are doing.",
		BootStarting:  "Handing over to the booted system...",

		CreateTitle: "Create snapshot", FieldName: "Name", FieldNote: "Note", FieldTag: "Tag", FieldFrom: "From",
		RollbackTitle: "Rollback", RollbackKeep: "Rollback (keep the node's own data)", RollbackDiscard: "Rollback with --discard (wipes the node's own data)",
		RollbackSure: "--discard permanently deletes the data written in this snapshot. Continue?",
		DeleteTitle:  "Delete snapshot", DeleteRec: "This also deletes all descendants (recursive):", DeleteCurrent: "This is the current snapshot; delete it anyway (--force)?",
		TagTitle: "Tag", TagHelp: "Tag to add (prefix with - to remove)", NoteTitle: "Note",
		PruneTitle: "Prune / GC", PruneRetention: "Prune by retention (keep_last / keep_tagged)", PruneGC: "GC ephemeral tmp-* snapshots older than 24h",
		PruneNothing: "Nothing to delete.", PruneAsk: "Delete these snapshots?",
		FlattenTitle: "Flatten", ExportTitle: "Export", FieldOut: "Output file", VerifyTitle: "Verify", VerifyDeep: "Deep verify (hash the base image, slow)?", VerifyOK: "verify: OK",

		SourcesTitle: "Sources", SourceTest: "Testing sources", SourceBlocked: "blocked by policy", SourceOffline: "offline only",
		CatalogTitle: "Catalog", FilterTitle: "Filter", FieldOS: "OS", FieldRelease: "Release", FieldType: "Type (container|vm)",
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
