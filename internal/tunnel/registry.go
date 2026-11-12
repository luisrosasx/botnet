package tunnel

// Registry conserva los streams logicos que deben sobrevivir a la
// reconexion (T-0401 AC3): al re-abrir la conexion, cada stream sticky se
// re-anuda con su mismo id y los no-sticky mueren con ErrStreamGone —
// error tipado, cero hangs.
type Registry struct {
	order  []uint32
	sticky map[uint32]bool
}

func NewRegistry() *Registry {
	return &Registry{sticky: map[uint32]bool{}}
}

// Register apunta un stream logico del agente. sticky=false: la sesion
// morre con la conexion (transaccional); sticky=true: se re-anuda.
func (r *Registry) Register(id uint32, sticky bool) {
	if _, ok := r.sticky[id]; !ok {
		r.order = append(r.order, id)
	}
	r.sticky[id] = sticky
}

// Restore aplica la politica sobre la sesion nueva: devuelve los streams
// re-anudados y los que murieron con su error tipado.
func (r *Registry) Restore(s *Session) (reattached map[uint32]*Stream, lost map[uint32]error) {
	reattached = map[uint32]*Stream{}
	lost = map[uint32]error{}
	for _, id := range r.order {
		if r.sticky[id] {
			if st, err := s.OpenWith(id); err == nil {
				reattached[id] = st
				continue
			}
		}
		lost[id] = ErrStreamGone
	}
	return reattached, lost
}

// IDs expone la lista determinista para logs y asserts.
func (r *Registry) IDs() []uint32 { return append([]uint32(nil), r.order...) }
