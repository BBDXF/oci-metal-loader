package tui

// Strings holds every user-visible UI string (English). Translating the TUI later means providing another Strings value;
// no other file contains UI text (key names and data values excepted).
type Strings struct {
	Title, Tabs                                                    []string
	AppName                                                        string
	HintImages, HintDetail, HintPull, HintStorage, HintNet         string
	HintSettings, HintOp, HintDialog, HintForm, HintHelp           string
	NoImages, NoSnaps, Loading, Refreshed, Cancelled, Running      string
	Done, Failed, Close, Yes, No, Cancel, Warning, Error, Info     string
	ColName, ColType, ColSize, ColBase, ColSnaps, ColCurrent       string
	ColUsed                                                        string
	BaseNode, CurrentMark, Frozen, Leaf, Ephemeral, NoLayer        string
	QuitTitle, QuitText                                            string
	PowerTitle, PowerReboot, PowerOff, PowerRebooting, PowerBusy   string
	BootTitle, BootCurrent, BootSelected, BootThrowaway, BootBase  string
	BootConfirm, BootPreview, BootStarting                         string
	BootContinues, BootEphemeralNote                               string
	CurrentNode, EphemeralNode                                     string
	CreateTitle, FieldName, FieldNote, FieldTag, CreateHelp        string
	RevertTitle, RevertKeep, RevertDiscard, RevertSure             string
	RevertSelect, RevertKeepHelp                                   string
	DeleteTitle, DeleteRec, DeleteCurrent, DeleteBase              string
	DeleteNotSnap, DeleteMerge, TagNotSnap, SelectSnap             string
	TagTitle, TagHelp, NoteTitle                                   string
	PruneTitle, PruneRetention, PruneGC, PruneNothing, PruneAsk    string
	FlattenTitle, ExportTitle, FieldOut, VerifyTitle, VerifyDeep   string
	VerifyOK                                                       string
	SourcesTitle, SourceTest, SourceBlocked, SourceOffline         string
	CatalogTitle, FilterTitle, FieldSearch, FilterHelp, MatchCount string
	PullTitle, PullConfirm, PullName, PullProgress, NoSources      string
	PullOffline, NoCatalog                                         string
	StoreTitle, StoreNone, StoreCreate, StoreGrow, StoreSize       string
	StorePath, StoreWizard, StoreGuard, StoreGuardOK               string
	StoreGuardLow, StoreDirty, StoreClean                          string
	NetTitle, SettingsTitle                                        string
	HelpTitle                                                      string
	HelpPages                                                      []HelpPage
	HelpScroll                                                     string
	Working, HdrVersion, HdrStore, HdrFree, HdrRAM, HdrImages      string
	HdrNet, HdrNoNet, HdrOffline, OpLog, OpRunning, OpOK, OpFail   string
	OpElapsed, OpDismiss, OpCancelled, OpCancelHint, OpLogOff      string
	HintPullCat, HintOpRun, HintOpDone, HintMsg, HintMenu          string
	SearchLabel, SearchEmpty, NoMatch, Matches                     string
	RevertWhat, RevertTarget, DeleteWhat, PruneWhat, GCWhat        string
	StoreCreateWarn, StoreGrowWarn, StoreCreateAsk, StoreGrowAsk   string
	InfoTitle, OpSuccessText, RetWhat, PosFmt, HelpHere            string
	ASCIINote                                                      string
	TooSmall                                                       string
}

// HelpPage is one section of the full-screen help.
type HelpPage struct {
	Title string
	Lines []string
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
		HintPull:     "Enter:load catalog  m:mirror  t:test  /:search  r:reload  Tab:tabs  Esc:back  ?:help",
		HintStorage:  "c:create/grow store file  r:refresh  1-5:tabs  ?:help",
		HintNet:      "r:refresh  t:test sources  1-5:tabs  ?:help",
		HintSettings: "Up/Down:scroll  1-5:tabs  ?:help",
		HintOp:       "Ctrl+C:cancel  l:log  Up/Down:scroll  Enter/Esc:close when finished",
		HintDialog:   "Enter:ok  Esc:cancel",
		HintForm:     "Tab/Down:next field  Enter:ok  Esc:cancel",
		HintHelp:     "Up/Down:scroll  Esc/q/?:close",

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
		RevertSure:     "The changes of the current live state are discarded permanently (CANNOT be undone).",
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
		CatalogTitle: "Catalog", FilterTitle: "Filter", FieldSearch: "Search", FilterHelp: "Keywords, all must match os/release/variant/arch/type (e.g. arch vm). Empty = all.", MatchCount: "%d of %d image(s) match",
		PullTitle: "Pull image", PullConfirm: "Download and import this image?", PullName: "Image name", PullProgress: "downloaded", NoSources: "No sources configured.",
		PullOffline: "Online access is disabled by policy or offline mode; only cached images can be used.", NoCatalog: "No catalog loaded. Press Enter on a source.",

		StoreTitle: "Storage", StoreNone: "no store file configured/found", StoreCreate: "create", StoreGrow: "grow", StoreSize: "Size (e.g. 20G)",
		StorePath: "Store file path", StoreWizard: "Store file wizard", StoreGuard: "Space guard", StoreGuardOK: "ok", StoreGuardLow: "BELOW min_free_percent: create/boot are refused",
		StoreDirty: "NOT clean (run `omloader store check`)", StoreClean: "clean",

		NetTitle: "Network", SettingsTitle: "Settings (read-only, resolved config with source layer)",
		HelpTitle:  "Help",
		HelpScroll: "Up/Down/PgUp/PgDn: scroll    Esc / q / ?: close",
		HelpPages: []HelpPage{
			{"Everywhere", []string{
				"1-5 / Tab / Shift+Tab   switch tab (Images Pull Storage Network Settings)",
				"Up/Down j/k  PgUp/PgDn  Home/End   move in lists (position shown as n/total, scroll bar on the right)",
				"Enter select / ok     Esc back / cancel     ?  or F1  this help     Ctrl+L redraw",
				"s shell (exit the shell to return)     P reboot / power off     q or Esc on the first screen: leave",
				"Ctrl+C cancels a running operation; it never quits the TUI.",
				"Header row: loader version, snapshot store free space, images size, network (IP / offline).",
				"Bottom bar: the keys valid right now.  Boot hands over to the booted system.",
			}},
			{"Images", []string{
				"Enter / o   open the image (snapshot tree)",
				"r           refresh the list",
			}},
			{"Image detail (snapshot tree)", []string{
				"Enter / b   boot menu: continue current, selected snapshot (ephemeral), throwaway, base (ephemeral)",
				"c   create snapshot (freeze the live state)     t  tag (-tag removes)     n  note",
				"r   revert to the selected snapshot / base: the CURRENT LIVE STATE IS DISCARDED (or kept as a snapshot)",
				"d   delete snapshot (merged into its child on overlay images; refused on dm-snapshot: flatten first)",
				"p   prune by retention / GC of ephemeral leftovers (dry-run first)",
				"f   flatten into a new image     e  export     v  verify (deep = hash the base)     R  reload",
				"Colours: base bold, ● current green, frozen cyan, ephemeral yellow, #tags magenta, sizes dim.",
			}},
			{"Pull", []string{
				"Sources list: Enter load catalog, t test sources, / go to the search box, r reload, Esc back",
				"Catalog: just type to filter live (several words must all match os/release/variant/arch/type,",
				"  e.g. 'arch vm', 'debian 12 cloud'); Backspace edits; Esc clears the search, a second Esc goes",
				"  back to the sources; Up/Down PgUp/PgDn Home/End move; Enter pull the selected image.",
				"While the search box has focus letters and digits are text: use Tab / Shift+Tab to change tab.",
			}},
			{"Operations (pull, export, flatten, store, verify, ...)", []string{
				"Progress bar with speed and ETA when the size is known, a spinner otherwise.",
				"Ctrl+C cancel     l show / hide the log     Up/Down PgUp/PgDn Home/End scroll the log",
				"When finished a result panel stays (success / failure, elapsed time): Enter or Esc dismisses it.",
			}},
			{"Storage / Network / Settings", []string{
				"Storage: c create / grow the store file wizard (confirmation explains the effect), r refresh",
				"Network: r refresh, t also probe the sources     Settings: read-only resolved config, scroll",
			}},
			{"Dialogs", []string{
				"Confirm: Enter / y = yes, Esc / n = no (destructive actions say what is lost).  Menus: Up/Down, Enter or 1-9.",
				"Forms: Tab / Down next field, Shift+Tab / Up previous, Enter on the last field submits, Esc cancels.",
			}},
		},
		Working: "working", HdrVersion: "omloader", HdrStore: "store", HdrFree: "free", HdrRAM: "RAM only (not persistent)", HdrImages: "images used", HdrNet: "net", HdrNoNet: "no address", HdrOffline: "[offline]",
		OpLog: "log", OpRunning: "RUNNING", OpOK: "SUCCESS", OpFail: "FAILED", OpElapsed: "elapsed", OpDismiss: "Enter/Esc: dismiss", OpCancelled: "CANCELLED", OpCancelHint: "Ctrl+C: cancel", OpLogOff: "log hidden (l shows it)",
		HintPullCat: "type:search  Esc:clear/back  Up/Dn PgUp/PgDn:move  Enter:pull  Tab:tabs  F1:help",
		HintOpRun:   "Ctrl+C:cancel  l:log on/off  Up/Down PgUp/PgDn:scroll log",
		HintOpDone:  "Enter/Esc:dismiss  l:log on/off  Up/Down PgUp/PgDn:scroll log",
		HintMsg:     "Enter/Esc:close",
		HintMenu:    "Up/Down:choose  Enter:ok  1-9:quick pick  Esc:cancel",
		SearchLabel: "Search", SearchEmpty: "(type to filter)", NoMatch: "No image matches the search.", Matches: "%d of %d",
		RevertWhat: "The current live state of this image (all changes made on top of %s) will be DISCARDED and cannot be recovered.", RevertTarget: "Afterwards the image continues from %s. %s itself is not modified.",
		DeleteWhat: "The snapshot %q and its %s layer will be removed permanently.", PruneWhat: "dry-run result: would delete %d layer(s); they are removed permanently:", GCWhat: "Garbage collection only removes leftovers of ephemeral boots; snapshots and the live state are kept.",
		StoreCreateWarn: "Creates an ext4 store file (label OMLOADER) on the host partition.", StoreGrowWarn: "Grows the store file offline (extend + e2fsck + resize2fs). The loader releases (unmounts) the store first: reboot afterwards.",
		StoreCreateAsk: "Create %s with size %s? Needs that much free space on the host partition; the store is used after a reboot.", StoreGrowAsk: "Grow %s to %s? The store is RELEASED (unmounted) now: snapshot operations stop until you reboot the loader.",
		PosFmt: "%d/%d", HelpHere: "this screen", InfoTitle: "Selected", OpSuccessText: "Completed without errors.", RetWhat: "Retention prune removes snapshots beyond keep_last / keep_tagged; the live state is kept.",
		ASCIINote: "ASCII",
		TooSmall:  "Terminal too small (need 80x24)",
	}
}
