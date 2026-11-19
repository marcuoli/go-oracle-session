package diagnostic

// walkLeaves shares the bounded traversal used by both diagnostic projections.
// It never formats wrapper prose or invokes recursive errors.As/Is/Timeout hooks.
func walkLeaves(err error, connection bool, visit func(error, ConnectionKind) error) error {
	type node struct {
		failure error
		kind    ConnectionKind
	}
	pending := []node{{err, ConnectionUnknown}}
	visited := 0
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current.failure == nil {
			continue
		}
		visited++
		if visited > maxNodes {
			return ErrCapacity
		}
		if connection {
			current.kind = inheritKind(current.kind, classifyConnection(current.failure))
		}
		if joined, ok := current.failure.(interface{ Unwrap() []error }); ok {
			children := joined.Unwrap()
			if len(children)+len(pending) > maxNodes {
				return ErrCapacity
			}
			if len(children) > 0 {
				for i := len(children) - 1; i >= 0; i-- {
					pending = append(pending, node{children[i], current.kind})
				}
				continue
			}
		}
		if wrapped, ok := current.failure.(interface{ Unwrap() error }); ok {
			if child := wrapped.Unwrap(); child != nil {
				pending = append(pending, node{child, current.kind})
				continue
			}
		}
		if err := visit(current.failure, current.kind); err != nil {
			return err
		}
	}
	return nil
}
