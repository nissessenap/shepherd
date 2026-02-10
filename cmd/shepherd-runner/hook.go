package main

import "fmt"

type HookCmd struct{}

func (c *HookCmd) Run() error {
	return fmt.Errorf("not implemented")
}
