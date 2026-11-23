package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	pc "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	s "github.com/HappyLadySauceM/Nagi/pkg/db/ent/systemsetting"
)

type Repo struct{ pool *db.Pool }

func NewRepo(p *db.Pool) (*Repo, error) {
	if p == nil {
		return nil, fmt.Errorf("settings: pool required")
	}
	return &Repo{p}, nil
}
func (r *Repo) Load(ctx context.Context) (settingsdomain.Revision, bool, error) {
	v, err := r.pool.Client.SystemSetting.Get(ctx, 1)
	if ent.IsNotFound(err) {
		return settingsdomain.Revision{}, false, nil
	}
	if err != nil {
		return settingsdomain.Revision{}, false, err
	}
	out, err := revision(v)
	return out, err == nil, err
}
func (r *Repo) Insert(ctx context.Context, settings settingsdomain.Settings, actor string) (settingsdomain.Revision, error) {
	payload, err := json.Marshal(settings)
	if err != nil {
		return settingsdomain.Revision{}, err
	}
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return settingsdomain.Revision{}, err
	}
	defer tx.Rollback()
	client := tx.Client()
	if _, err = client.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('settings:bootstrap',0))"); err != nil {
		return settingsdomain.Revision{}, err
	}
	if err = client.SystemSetting.Create().SetID(1).SetPayload(payload).SetVersion(1).SetUpdatedBy(actor).OnConflict(entsql.ConflictColumns("id")).Ignore().Exec(ctx); err != nil {
		return settingsdomain.Revision{}, err
	}
	if err = tx.Commit(); err != nil {
		return settingsdomain.Revision{}, err
	}
	out, _, err := r.Load(ctx)
	return out, err
}
func (r *Repo) Update(ctx context.Context, settings settingsdomain.Settings, expected int64, actor string) (settingsdomain.Revision, bool, error) {
	payload, err := json.Marshal(settings)
	if err != nil {
		return settingsdomain.Revision{}, false, err
	}
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return settingsdomain.Revision{}, false, err
	}
	defer tx.Rollback()
	prior, err := tx.Client().SystemSetting.Query().Where(s.ID(1), s.Version(expected)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return settingsdomain.Revision{}, false, nil
	}
	if err != nil {
		return settingsdomain.Revision{}, false, err
	}
	before, err := revision(prior)
	if err != nil {
		return settingsdomain.Revision{}, false, err
	}
	generations := pc.AdvancePermissionGenerations(prior.SendPermitGenerations, before.Settings.Tools.PermissionDefaults, settings.Tools.PermissionDefaults)
	n, err := tx.Client().SystemSetting.Update().Where(s.ID(1), s.Version(expected)).SetPayload(payload).SetSendPermitGenerations(generations).SetUpdatedBy(actor).SetUpdatedAt(time.Now()).AddVersion(1).Save(ctx)
	if err != nil {
		return settingsdomain.Revision{}, false, err
	}
	if n != 1 {
		return settingsdomain.Revision{}, false, nil
	}
	v, err := tx.Client().SystemSetting.Get(ctx, 1)
	if err != nil {
		return settingsdomain.Revision{}, false, err
	}
	out, err := revision(v)
	if err != nil {
		return out, false, err
	}
	err = tx.Commit()
	return out, err == nil, err
}
func (r *Repo) Version(ctx context.Context) (int64, bool, error) {
	v, err := r.pool.Client.SystemSetting.Query().Where(s.ID(1)).Select(s.FieldVersion).Only(ctx)
	if ent.IsNotFound(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v.Version, true, nil
}
func revision(v *ent.SystemSetting) (settingsdomain.Revision, error) {
	out := settingsdomain.Revision{Version: v.Version, UpdatedAt: v.UpdatedAt, UpdatedBy: v.UpdatedBy}
	err := json.Unmarshal(v.Payload, &out.Settings)
	return out, err
}
