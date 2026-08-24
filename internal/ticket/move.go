package ticket

import (
	"errors"
	"fmt"
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// moveTicketFile publishes the complete destination without replacing an
// existing file. It removes the source only after the destination exists and
// removes the destination again if source removal fails.
func moveTicketFile(source, destination string, data []byte, perm os.FileMode) error {
	if err := store.WriteFileExclusiveAtomic(destination, data, perm, "Ticket"); err != nil {
		if errors.Is(err, os.ErrExist) {
			return clierr.New(clierr.AlreadyExists, "ticket file %q already exists", destination)
		}
		return err
	}
	if err := os.Remove(source); err != nil {
		rollbackErr := os.Remove(destination)
		if rollbackErr != nil {
			return fmt.Errorf("remove moved ticket %q: %w; remove rollback destination %q: %v", source, err, destination, rollbackErr)
		}
		return fmt.Errorf("remove moved ticket %q: %w", source, err)
	}
	return nil
}
