package repowrite

// Symlink creates the link itself inside the pinned root, with the directory
// checked on both sides. Its target is never traversed by a write; a later
// write through a link that leaves the root is refused.
func (r *PinnedRoot) Symlink(target, relative string) error {
	clean, err := Relative(relative)
	if err != nil {
		return err
	}
	dir, name := splitTarget(clean)
	parent, err := r.hold(dir)
	if err != nil {
		return describe(err, "link", r.anchor.path, joinRel(r.rel, clean))
	}
	defer parent.close()
	return describe(parent.symlink(target, name), "link", r.anchor.path, joinRel(r.rel, clean))
}
