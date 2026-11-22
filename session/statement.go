package session

import "errors"

// validateStatement applies the same catalog reference and text bounds to
// managed and borrowed execution.
func validateStatement(statement Statement) error {
	if err := statement.Reference.Validate(); err != nil {
		return err
	}
	if statement.Text == "" || len(statement.Text) > 1<<20 {
		return errors.New("invalid catalog statement")
	}
	return nil
}

// statementText keeps reference validation and text bounds identical in both
// policies. Exact-text callers retain their approval/hash contract without a
// connector rewrite; the zero-value policy keeps existing reference comments.
func (s *Session) statementText(statement Statement) (string, error) {
	if err := validateStatement(statement); err != nil {
		return "", err
	}
	if s.options.PreserveStatementText {
		return statement.Text, nil
	}
	return statement.Reference.Annotate(statement.Text)
}
