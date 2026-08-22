package microvm

import "context"

// MockVM is a test double for the VM interface.
type MockVM struct {
	StartFunc     func(ctx context.Context) error
	StopFunc      func(ctx context.Context) error
	StatusFunc    func() Status
	CIDFunc       func() uint32
	VsockPortFunc func() uint32
	WaitFunc      func() error
}

func (m *MockVM) Start(ctx context.Context) error {
	if m.StartFunc != nil {
		return m.StartFunc(ctx)
	}
	return nil
}

func (m *MockVM) Stop(ctx context.Context) error {
	if m.StopFunc != nil {
		return m.StopFunc(ctx)
	}
	return nil
}

func (m *MockVM) Status() Status {
	if m.StatusFunc != nil {
		return m.StatusFunc()
	}
	return Stopped
}

func (m *MockVM) CID() uint32 {
	if m.CIDFunc != nil {
		return m.CIDFunc()
	}
	return 3
}

func (m *MockVM) VsockPort() uint32 {
	if m.VsockPortFunc != nil {
		return m.VsockPortFunc()
	}
	return 1024
}

func (m *MockVM) Wait() error {
	if m.WaitFunc != nil {
		return m.WaitFunc()
	}
	return nil
}
