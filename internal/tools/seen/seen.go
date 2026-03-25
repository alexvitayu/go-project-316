package seen

import "sync"

type Visits struct {
	Mu        *sync.Mutex
	IsVisited map[string]struct{}
}

func NewVisits() *Visits {
	return &Visits{
		IsVisited: make(map[string]struct{}),
		Mu:        &sync.Mutex{},
	}
}
