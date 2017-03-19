package service

var DefaultRegistry = new(Registry)

func Register(r *Registry, name string, version uint32, f Factory) {
	if r == nil {
		r = DefaultRegistry
	}
	r.Register(name, version, f)
}

func RegisterFunc(r *Registry, name string, version uint32, f func(evs chan<- []byte) Instance) {
	Register(r, name, version, FactoryFunc(f))
}
