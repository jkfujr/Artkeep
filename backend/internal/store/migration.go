package store

import "collection/internal/model"

// 只切换文件位置，保留作品、媒体及所有人工信息。
func (s *Store) Relocate(w model.Work, assets []model.Asset) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, a := range assets {
		if _, err = tx.Exec("UPDATE assets SET root=?,path=? WHERE id=? AND work_id=?", a.Root, a.Path, a.ID, w.ID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE works SET root=?,metadata_path=? WHERE id=?", w.Root, w.MetadataPath, w.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) MigrationIDs() ([]int64, error) {
	rows, err := s.DB.Query("SELECT id FROM works WHERE root<>'collection' OR metadata_path NOT LIKE '%/metadata/source.json' OR EXISTS(SELECT 1 FROM assets a WHERE a.work_id=works.id AND (a.root<>'collection' OR a.path NOT LIKE '%/media/%')) ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
