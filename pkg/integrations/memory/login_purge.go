package memory

import (
	"context"
	"errors"

	"go.mau.fi/util/dbutil"
)

func PurgeTables(ctx context.Context, db *dbutil.Database, bridgeID, loginID string) error {
	if db == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var purgeErrs []error
	exec := func(query string, args ...any) {
		if _, err := db.Exec(ctx, query, args...); err != nil {
			purgeErrs = append(purgeErrs, err)
		}
	}
	exec(`DELETE FROM aichats_memory_chunks_fts WHERE bridge_id=$1 AND login_id=$2`, bridgeID, loginID)
	exec(`DELETE FROM aichats_memory_session_files WHERE bridge_id=$1 AND login_id=$2`, bridgeID, loginID)
	exec(`DELETE FROM aichats_memory_session_state WHERE bridge_id=$1 AND login_id=$2`, bridgeID, loginID)
	exec(`DELETE FROM aichats_memory_embedding_cache WHERE bridge_id=$1 AND login_id=$2`, bridgeID, loginID)
	exec(`DELETE FROM aichats_memory_chunks WHERE bridge_id=$1 AND login_id=$2`, bridgeID, loginID)
	exec(`DELETE FROM aichats_memory_files WHERE bridge_id=$1 AND login_id=$2`, bridgeID, loginID)
	exec(`DELETE FROM aichats_memory_meta WHERE bridge_id=$1 AND login_id=$2`, bridgeID, loginID)
	return errors.Join(purgeErrs...)
}
