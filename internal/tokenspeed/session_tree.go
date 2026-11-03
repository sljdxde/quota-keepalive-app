package tokenspeed

import "strings"

// normalizeSessionRef strips the on-disk "session-" directory prefix from a
// parent session reference so it matches dshSessionID's normalized form.
func normalizeSessionRef(ref string) string {
	ref = strings.TrimSpace(ref)
	return strings.TrimPrefix(ref, "session-")
}

// rootSessionID follows parentSession links to the top-level conversation a
// sub-agent session belongs to. Cycles or missing links stop at the current id.
func (s *Service) rootSessionID(session string) string {
	current := session
	for i := 0; i < 64; i++ {
		parent := s.sessionParent[current]
		if parent == "" || parent == current {
			return current
		}
		current = parent
	}
	return current
}

// canonicalAgentID folds a DSH sub-agent session into its top-level session so
// the token panel shows one row per conversation instead of one per sub-agent.
// Other sources keep their existing identity.
func (s *Service) canonicalAgentID(agentID string) string {
	if !strings.HasPrefix(agentID, "dsh:") {
		return agentID
	}
	session := strings.TrimPrefix(agentID, "dsh:")
	root := s.rootSessionID(session)
	if root == session {
		return agentID
	}
	return "dsh:" + root
}

// sessionLabel prefers the conversation's own title (from session/title
// events); otherwise it falls back to project, then model, then a short id.
func (s *Service) sessionLabel(agentID, project, model string) string {
	if strings.HasPrefix(agentID, "dsh:") {
		session := strings.TrimPrefix(agentID, "dsh:")
		if title := strings.TrimSpace(s.sessionTitles[session]); title != "" {
			return title
		}
	}
	return sessionDisplayLabel(project, model, agentID)
}
