package tui

// InitRow is one field of the first-boot init form (Source: "image" or "default config").
type InitRow struct{ Key, Value, Source string }

// Initer is optionally implemented by a Service: the first-boot init that every image needs once before it boots.
type Initer interface {
	InitNeeded(image string) bool
	InitPrefill(image string) []InitRow
	InitSave(image string, values []string) error // values in InitPrefill order
}

const initNotice = "Image not initialised, confirm to boot."

// initGate returns true when the image still needs its init: the form is then opened and boot runs after it is confirmed.
func (m *Model) initGate(image, notice string, boot func()) bool {
	in, ok := m.Svc.(Initer)
	if !ok || !in.InitNeeded(image) {
		return false
	}
	rows := in.InitPrefill(image)
	labels, vals := make([]string, len(rows)), make([]string, len(rows))
	for i, r := range rows {
		labels[i], vals[i] = r.Key+" ["+r.Source+"]", r.Value
	}
	lines := []string{notice, "Fields marked [image] keep the image's own value unless you change them."}
	m.form("First boot: "+image, lines, labels, vals, func(v []string) {
		if err := in.InitSave(image, v); err != nil {
			m.dlg = nil
			m.fail(err)
			return
		}
		m.dlg = nil
		boot()
	})
	return true
}
