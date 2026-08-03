package session

// SupersededMap scans a step list for revisions (isRevision + revisesStep) and
// returns, for every step number that got revised, the step number of the
// LATEST step that revises it. This is a read-time derivation only — it never
// touches the stored steps themselves; callers attach the result to a COPY of
// the step(s) they return (#512). Revision stays additive/audit-trail: nothing
// about the superseded step is mutated or deleted, this only makes the forward
// link ("step M revises step N") discoverable backward ("step N superseded by
// step M") without a second lookup.
//
// If more than one later step revises the same target, the one with the
// highest StepNumber wins (steps are appended in increasing StepNumber order,
// so a simple forward scan with overwrite-on-match yields "latest").
func SupersededMap(steps []ResearchStep) map[int]int {
	var out map[int]int
	for _, s := range steps {
		if !s.IsRevision || s.RevisesStep <= 0 {
			continue
		}
		if out == nil {
			out = make(map[int]int)
		}
		if prev, ok := out[s.RevisesStep]; !ok || s.StepNumber > prev {
			out[s.RevisesStep] = s.StepNumber
		}
	}
	return out
}

// applySupersededBy returns a copy of steps with SupersededBy populated from an
// already-computed supersededBy map (typically SupersededMap over the FULL step
// list, so a subset view like the last-3-steps window still gets the right
// answer even when the revising step falls outside that window). Never mutates
// the input slice/structs.
func applySupersededBy(steps []ResearchStep, sm map[int]int) []ResearchStep {
	if len(steps) == 0 {
		return steps
	}
	out := make([]ResearchStep, len(steps))
	for i, s := range steps {
		if by, ok := sm[s.StepNumber]; ok {
			s.SupersededBy = by
		}
		out[i] = s
	}
	return out
}

// WithSupersededBy computes SupersededMap over steps and applies it back to
// steps. Use this when the slice passed in already IS the full step list (e.g.
// rendering a complete export) so the map's inputs and the copy being annotated
// are the same set.
func WithSupersededBy(steps []ResearchStep) []ResearchStep {
	return applySupersededBy(steps, SupersededMap(steps))
}

// ApplySupersededByTo annotates a single step (typically fetched in isolation,
// e.g. GetStep) using a supersededBy map computed from the FULL session step
// list it belongs to. Returns a copy; the original is never mutated.
func ApplySupersededByTo(step ResearchStep, sm map[int]int) ResearchStep {
	if by, ok := sm[step.StepNumber]; ok {
		step.SupersededBy = by
	}
	return step
}
