-- Nullable numerical projections preserve missing measurements (NULL != 0).
-- Existing rows stay unprojected until the resumable backfill completes.
ALTER TABLE aegis_measurements
  ADD COLUMN cpu_used_percent DOUBLE PRECISION,
  ADD COLUMN memory_used_percent DOUBLE PRECISION,
  ADD COLUMN disk_used_percent DOUBLE PRECISION,
  ADD COLUMN disk_path TEXT,
  ADD COLUMN projected BOOLEAN NOT NULL DEFAULT FALSE;

-- Only rows awaiting a backfill are indexed (new writes are projected=true).
CREATE INDEX aegis_measurements_unprojected_idx
  ON aegis_measurements (agent_id, measurement_id)
  WHERE projected = FALSE;
