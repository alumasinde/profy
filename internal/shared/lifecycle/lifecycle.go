// Package lifecycle defines the shared draft → published → archived content lifecycle and
// which permission each transition needs (plan §5.4: distinct publish and archive permissions).
package lifecycle

// Statuses.
const (
	Draft     = "draft"
	Published = "published"
	Archived  = "archived"
)

// Valid reports whether s is a lifecycle status.
func Valid(s string) bool { return s == Draft || s == Published || s == Archived }

// Permission returns the permission needed to move from one status to another, using the
// module's permission prefix (for example "pages" → "pages.publish"). Publishing,
// unpublishing and restoring need <prefix>.publish; archiving needs <prefix>.archive.
func Permission(prefix, from, to string) string {
	switch {
	case to == Archived:
		return prefix + ".archive"
	case to == Published, from == Published, from == Archived:
		return prefix + ".publish"
	}
	return prefix + ".update"
}
