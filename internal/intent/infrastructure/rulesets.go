package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
	ra "github.com/HappyLadySauceM/Nagi/pkg/db/ent/intentruleactivation"
	re "github.com/HappyLadySauceM/Nagi/pkg/db/ent/intentruleentry"
	rs "github.com/HappyLadySauceM/Nagi/pkg/db/ent/intentruleset"
	"time"
)

var ErrRulesetNotFound = errors.New("intent: ruleset not found")
var ErrTargetConflict = errors.New("intent: target already bound to another ruleset")
var ErrRulesetImmutable = errors.New("intent: ruleset is not a draft")

type RulesetsRepo struct{ pool *db.Pool }

func NewRulesetsRepo(p *db.Pool) (*RulesetsRepo, error) {
	if p == nil {
		return nil, fmt.Errorf("intent: pool required")
	}
	return &RulesetsRepo{p}, nil
}
func (r *RulesetsRepo) Active(ctx context.Context, bot int64, kind string, id int64) (domain.Ruleset, bool, error) {
	a, err := r.pool.Client.IntentRuleActivation.Query().Where(ra.BotID(bot), ra.ScopeType(kind), ra.ScopeID(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.Ruleset{}, false, nil
	}
	if err != nil {
		return domain.Ruleset{}, false, err
	}
	v, err := r.Get(ctx, a.RulesetID)
	if errors.Is(err, ErrRulesetNotFound) {
		return domain.Ruleset{}, false, nil
	}
	return v, err == nil, err
}
func (r *RulesetsRepo) List(ctx context.Context, bot int64, kind string, id int64) ([]domain.Ruleset, error) {
	q := r.pool.Client.IntentRuleset.Query().Where(rs.BotID(bot), rs.DeletedAtIsNil())
	if kind != "" {
		q.Where(rs.ScopeType(kind), rs.ScopeID(id))
	}
	rows, err := q.Order(ent.Desc(rs.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.Ruleset
	for _, v := range rows {
		model, err := rulesetModel(v)
		if err != nil {
			return nil, err
		}
		out = append(out, model)
	}
	return out, nil
}
func (r *RulesetsRepo) Get(ctx context.Context, id int64) (domain.Ruleset, error) {
	v, err := r.pool.Client.IntentRuleset.Query().Where(rs.ID(id), rs.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return domain.Ruleset{}, ErrRulesetNotFound
	}
	if err != nil {
		return domain.Ruleset{}, err
	}
	out, err := rulesetModel(v)
	if err != nil {
		return out, err
	}
	out.Entries, err = ruleEntries(ctx, r.pool.Client, id)
	return out, err
}
func rulesetLock(ctx context.Context, c *ent.Client, bot int64) error {
	_, err := c.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", fmt.Sprintf("intent:%d", bot))
	return err
}
func (r *RulesetsRepo) CreateDraft(ctx context.Context, bot int64, kind string, scope int64, note string, cloneActive bool, cloneFrom ...int64) (domain.Ruleset, error) {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return domain.Ruleset{}, err
	}
	defer tx.Rollback()
	c := tx.Client()
	if err := rulesetLock(ctx, c, bot); err != nil {
		return domain.Ruleset{}, err
	}
	q := c.IntentRuleset.Query().Where(rs.BotID(bot), rs.ScopeType(kind), rs.ScopeID(scope))
	latest, err := q.Clone().Order(ent.Desc(rs.FieldVersion)).First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return domain.Ruleset{}, err
	}
	version := 1
	if latest != nil {
		version = latest.Version + 1
	}
	b := c.IntentRuleset.Create().SetBotID(bot).SetScopeType(kind).SetScopeID(scope).SetVersion(version).SetStatus("draft").SetNote(note)
	if scope > 0 {
		data, _ := json.Marshal([]domain.RuleTarget{{Kind: kind, ID: scope}})
		b.SetTargets(data)
	}
	var source *ent.IntentRuleset
	if cloneActive || (len(cloneFrom) > 0 && cloneFrom[0] > 0) {
		sourceQ := q.Clone().Where(rs.DeletedAtIsNil())
		explicit := len(cloneFrom) > 0 && cloneFrom[0] > 0
		if explicit {
			sourceQ.Where(rs.ID(cloneFrom[0]), rs.StatusIn("draft", "published", "archived"))
		} else {
			sourceQ.Where(rs.Status("published"))
		}
		source, err = sourceQ.Order(ent.Desc(rs.FieldVersion)).First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return domain.Ruleset{}, err
		}
		if explicit && ent.IsNotFound(err) {
			return domain.Ruleset{}, ErrRulesetNotFound
		}
		if source != nil {
			b.SetTargets(source.Targets).SetBuiltinRules(source.BuiltinRules)
		}
	}
	v, err := b.Save(ctx)
	if err != nil {
		return domain.Ruleset{}, err
	}
	if source != nil {
		entries, err := c.IntentRuleEntry.Query().Where(re.RulesetID(source.ID)).All(ctx)
		if err != nil {
			return domain.Ruleset{}, err
		}
		for _, entry := range entries {
			if err := c.IntentRuleEntry.Create().SetRulesetID(v.ID).SetPriority(entry.Priority).SetMatch(entry.Match).SetAction(entry.Action).SetEnabled(entry.Enabled).Exec(ctx); err != nil {
				return domain.Ruleset{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Ruleset{}, err
	}
	return r.Get(ctx, v.ID)
}

type RuleOptions struct {
	BuiltinRules map[string]bool
	Targets      *[]domain.RuleTarget
}

func (r *RulesetsRepo) ReplaceEntries(ctx context.Context, id int64, entries []domain.RuleEntry, options ...RuleOptions) error {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c := tx.Client()
	v, err := c.IntentRuleset.Query().Where(rs.ID(id), rs.DeletedAtIsNil()).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return ErrRulesetNotFound
	}
	if err != nil {
		return err
	}
	if v.Status != "draft" {
		return ErrRulesetImmutable
	}
	if len(options) > 0 {
		data, err := json.Marshal(options[0].BuiltinRules)
		if err != nil {
			return err
		}
		b := c.IntentRuleset.UpdateOneID(id).SetBuiltinRules(data)
		if options[0].Targets != nil {
			data, err = json.Marshal(*options[0].Targets)
			if err != nil {
				return err
			}
			b.SetTargets(data)
		}
		if err := b.Exec(ctx); err != nil {
			return err
		}
	}
	if _, err := c.IntentRuleEntry.Delete().Where(re.RulesetID(id)).Exec(ctx); err != nil {
		return err
	}
	for _, entry := range entries {
		match, err := json.Marshal(entry.Match)
		if err != nil {
			return err
		}
		action, err := json.Marshal(entry.Action)
		if err != nil {
			return err
		}
		if err := c.IntentRuleEntry.Create().SetRulesetID(id).SetPriority(entry.Priority).SetMatch(match).SetAction(action).SetEnabled(entry.Enabled == nil || *entry.Enabled).Exec(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (r *RulesetsRepo) Publish(ctx context.Context, id int64) error {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c := tx.Client()
	initial, err := c.IntentRuleset.Query().Where(rs.ID(id), rs.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return ErrRulesetNotFound
	}
	if err != nil {
		return err
	}
	if err := rulesetLock(ctx, c, initial.BotID); err != nil {
		return err
	}
	v, err := c.IntentRuleset.Query().Where(rs.ID(id), rs.DeletedAtIsNil()).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return ErrRulesetNotFound
	}
	if err != nil {
		return err
	}
	if v.Status == "published" {
		return nil
	}
	model, err := rulesetModel(v)
	if err != nil {
		return err
	}
	targets := model.Targets
	if len(targets) == 0 {
		targets = []domain.RuleTarget{{Kind: "group", ID: 0}}
	}
	for _, target := range targets {
		a, err := c.IntentRuleActivation.Query().Where(ra.BotID(v.BotID), ra.ScopeType(target.Kind), ra.ScopeID(target.ID)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if a != nil {
			other, err := c.IntentRuleset.Get(ctx, a.RulesetID)
			if err != nil {
				return err
			}
			if other.ScopeType != v.ScopeType || other.ScopeID != v.ScopeID {
				return ErrTargetConflict
			}
		}
	}
	family := rs.And(rs.BotID(v.BotID), rs.ScopeType(v.ScopeType), rs.ScopeID(v.ScopeID))
	ids, err := c.IntentRuleset.Query().Where(family).IDs(ctx)
	if err != nil {
		return err
	}
	if _, err := c.IntentRuleActivation.Delete().Where(ra.RulesetIDIn(ids...)).Exec(ctx); err != nil {
		return err
	}
	if _, err := c.IntentRuleset.Update().Where(family, rs.Status("published")).SetStatus("archived").Save(ctx); err != nil {
		return err
	}
	if err := c.IntentRuleset.UpdateOneID(id).SetStatus("published").SetPublishedAt(time.Now()).Exec(ctx); err != nil {
		return err
	}
	for _, target := range targets {
		if err := c.IntentRuleActivation.Create().SetBotID(v.BotID).SetScopeType(target.Kind).SetScopeID(target.ID).SetRulesetID(id).Exec(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (r *RulesetsRepo) Rollback(ctx context.Context, bot int64, kind string, id int64, version int) error {
	v, err := r.pool.Client.IntentRuleset.Query().Where(rs.BotID(bot), rs.ScopeType(kind), rs.ScopeID(id), rs.Version(version), rs.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return ErrRulesetNotFound
	}
	if err != nil {
		return err
	}
	if v.Status != "archived" {
		return ErrRulesetImmutable
	}
	return r.Publish(ctx, v.ID)
}
func (r *RulesetsRepo) Delete(ctx context.Context, bot, id int64) error {
	tx, err := r.pool.Client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c := tx.Client()
	if err := rulesetLock(ctx, c, bot); err != nil {
		return err
	}
	v, err := c.IntentRuleset.Query().Where(rs.ID(id), rs.BotID(bot)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return ErrRulesetNotFound
	}
	if err != nil {
		return err
	}
	if _, err := c.IntentRuleActivation.Delete().Where(ra.RulesetID(id)).Exec(ctx); err != nil {
		return err
	}
	if v.DeletedAt == nil {
		if err := c.IntentRuleset.UpdateOneID(id).SetDeletedAt(time.Now()).Exec(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func rulesetModel(v *ent.IntentRuleset) (domain.Ruleset, error) {
	out := domain.Ruleset{ID: v.ID, BotID: v.BotID, ScopeType: v.ScopeType, ScopeID: v.ScopeID, Version: v.Version, Status: v.Status, Note: v.Note}
	if len(v.BuiltinRules) > 0 {
		if err := json.Unmarshal(v.BuiltinRules, &out.BuiltinRules); err != nil {
			return out, err
		}
	}
	if len(v.Targets) > 0 {
		if err := json.Unmarshal(v.Targets, &out.Targets); err != nil {
			return out, err
		}
	}
	return out, nil
}
func ruleEntries(ctx context.Context, c *ent.Client, id int64) ([]domain.RuleEntry, error) {
	rows, err := c.IntentRuleEntry.Query().Where(re.RulesetID(id)).Order(ent.Asc(re.FieldPriority)).All(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.RuleEntry
	for _, v := range rows {
		enabled := v.Enabled
		entry := domain.RuleEntry{Priority: v.Priority, Enabled: &enabled}
		if err := json.Unmarshal(v.Match, &entry.Match); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(v.Action, &entry.Action); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}
