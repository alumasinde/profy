package providers

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/alumasinde/profy/internal/database"
	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/modules/audit"
	"github.com/alumasinde/profy/internal/modules/media"
	"github.com/alumasinde/profy/internal/modules/settings"
	"github.com/alumasinde/profy/internal/shared/ids"
	"github.com/alumasinde/profy/internal/shared/reqctx"
	"github.com/alumasinde/profy/internal/shared/text"
)

// UploadPhoto stores a profile, cover or portfolio image for the target provider.
// Profile and cover replace the previous image; portfolio images respect the quota and are
// pre-moderated on published listings when portfolio_moderation = "pre" (§17).
func (s *Service) UploadPhoto(ctx context.Context, t Target, purpose, caption, name string, data []byte) (*View, error) {
	if purpose != media.PurposeProfile && purpose != media.PurposeCover && purpose != media.PurposePortfolio {
		return nil, response.Validation(map[string]string{"purpose": "invalid"})
	}
	var id uint64
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.resolve(ctx, tx, t, true)
		if err != nil {
			return err
		}
		if err := s.require(ctx, t, r, "media.upload_own", "providers.update_any"); err != nil {
			return err
		}
		if err := editable(r); err != nil {
			return err
		}
		id = r.id
		capt, ok := text.CleanLine(caption, 200)
		if !ok && capt != "" {
			return response.Validation(map[string]string{"caption": "invalid"})
		}
		pending := false
		if purpose == media.PurposePortfolio {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_portfolio pp JOIN media_files m ON m.id = pp.media_id
				WHERE pp.provider_id = ? AND m.status <> 'deleted'`, r.id).Scan(&n); err != nil {
				return err
			}
			if n >= s.settings.Int(ctx, settings.KeyPortfolioMaxItems) {
				return response.Validation(map[string]string{"file": "too_many"})
			}
			pending = r.status == StatusPublished && s.settings.String(ctx, settings.KeyPortfolioModeration) == "pre"
		}
		m, err := s.media.Upload(ctx, tx, media.UploadInput{OwnerUserID: reqctx.PrincipalFrom(ctx).UserID, ProviderID: r.id,
			Purpose: purpose, Data: data, Name: name, Pending: pending})
		if err != nil {
			return err
		}
		switch purpose {
		case media.PurposeProfile, media.PurposeCover:
			col, old := "profile_media_id", r.profileMediaID
			if purpose == media.PurposeCover {
				col, old = "cover_media_id", r.coverMediaID
			}
			if _, err := tx.ExecContext(ctx, `UPDATE providers SET `+col+` = ? WHERE id = ?`, m.InternalID, r.id); err != nil {
				return err
			}
			if old != nil {
				if err := s.media.MarkDeleted(ctx, tx, *old); err != nil {
					return err
				}
			}
			if err := s.materialChange(ctx, tx, r, purpose+"_photo"); err != nil {
				return err
			}
		case media.PurposePortfolio:
			var pos int
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), -1) + 1 FROM provider_portfolio WHERE provider_id = ?`, r.id).Scan(&pos); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO provider_portfolio (provider_id, media_id, caption, position) VALUES (?, ?, ?, ?)`,
				r.id, m.InternalID, optional(capt), pos); err != nil {
				return err
			}
		}
		if _, err := s.refresh(ctx, tx, r.id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.media.uploaded", EntityType: "provider", EntityID: r.publicID.String(),
			NewValues: map[string]any{"purpose": purpose, "media": m.ID, "status": m.Status}})
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, t.Staff)
}

// DeletePhoto removes a profile, cover or portfolio image belonging to the target provider.
func (s *Service) DeletePhoto(ctx context.Context, t Target, mediaID string) (*View, error) {
	var id uint64
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.resolve(ctx, tx, t, true)
		if err != nil {
			return err
		}
		if err := s.require(ctx, t, r, "media.delete_own", "media.delete_any"); err != nil {
			return err
		}
		if err := editable(r); err != nil {
			return err
		}
		id = r.id
		m, err := s.media.ByPublicID(ctx, tx, mediaID)
		if err != nil || m.ProviderID == nil || *m.ProviderID != r.id || m.Purpose == media.PurposeDocument || m.Status == media.StatusDeleted {
			return response.ErrNotFound
		}
		switch {
		case r.profileMediaID != nil && *r.profileMediaID == m.InternalID:
			_, err = tx.ExecContext(ctx, `UPDATE providers SET profile_media_id = NULL WHERE id = ?`, r.id)
		case r.coverMediaID != nil && *r.coverMediaID == m.InternalID:
			_, err = tx.ExecContext(ctx, `UPDATE providers SET cover_media_id = NULL WHERE id = ?`, r.id)
		default:
			_, err = tx.ExecContext(ctx, `DELETE FROM provider_portfolio WHERE provider_id = ? AND media_id = ?`, r.id, m.InternalID)
		}
		if err != nil {
			return err
		}
		if err := s.media.MarkDeleted(ctx, tx, m.InternalID); err != nil {
			return err
		}
		if _, err := s.refresh(ctx, tx, r.id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "providers.media.deleted", EntityType: "provider", EntityID: r.publicID.String(),
			OldValues: map[string]any{"media": m.ID, "purpose": m.Purpose}})
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, t.Staff)
}

// PortfolioOrder sets captions and order (array order = display order).
type PortfolioOrder struct {
	ID      string `json:"id"`
	Caption string `json:"caption"`
}

// ArrangePortfolio reorders portfolio items and updates captions.
func (s *Service) ArrangePortfolio(ctx context.Context, t Target, items []PortfolioOrder) (*View, error) {
	var id uint64
	err := database.WithTx(ctx, s.db, func(tx *sql.Tx) error {
		r, err := s.resolve(ctx, tx, t, true)
		if err != nil {
			return err
		}
		if err := s.require(ctx, t, r, "media.upload_own", "providers.update_any"); err != nil {
			return err
		}
		if err := editable(r); err != nil {
			return err
		}
		id = r.id
		for i, it := range items {
			pub, err := ids.Parse(it.ID)
			if err != nil {
				return response.Validation(map[string]string{"items." + strconv.Itoa(i): "invalid"})
			}
			capt, ok := text.CleanLine(it.Caption, 200)
			if !ok && capt != "" {
				return response.Validation(map[string]string{"items." + strconv.Itoa(i) + ".caption": "invalid"})
			}
			res, err := tx.ExecContext(ctx, `UPDATE provider_portfolio pp JOIN media_files m ON m.id = pp.media_id
				SET pp.position = ?, pp.caption = ? WHERE pp.provider_id = ? AND m.public_id = ?`, i, optional(capt), r.id, pub)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				var exists int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_portfolio pp JOIN media_files m ON m.id = pp.media_id
					WHERE pp.provider_id = ? AND m.public_id = ?`, r.id, pub).Scan(&exists); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if exists == 0 {
					return response.Validation(map[string]string{"items." + strconv.Itoa(i): "invalid"})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.viewByID(ctx, id, t.Staff)
}
