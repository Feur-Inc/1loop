package terminal

import "context"

// NativeHost starts Android-owned PRoot instances on the PTYs created by Go.
// Their outer process can be stopped without interrupting another terminal.
type NativeHost interface {
	Start(context.Context, string, string, bool) (string, error)
	Wait(context.Context, string) (int, error)
	Close(context.Context, string) error
}
