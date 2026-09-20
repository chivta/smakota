package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/arvlas/calbot/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

type WeightPoint struct {
	MeasuredOn time.Time `json:"measured_on"`
	AverageKG  float64   `json:"average_kg"`
}

type WeightRepo struct {
	pool *pgxpool.Pool
}

func NewWeightRepo(pool *pgxpool.Pool) *WeightRepo {
	return &WeightRepo{pool: pool}
}

func (r *WeightRepo) Add(ctx context.Context, w domain.Weight) error {
	_, err := r.pool.Exec(ctx, `
		insert into weights (user_id, weight_kg, measured_on)
		values ($1, $2, $3)
		on conflict (user_id, measured_on) do update
		set weight_kg = excluded.weight_kg`,
		w.UserID, w.WeightKG, dayString(w.MeasuredOn))
	if err != nil {
		return fmt.Errorf("add weight: %w", err)
	}
	return nil
}

func (r *WeightRepo) RollingAverage(ctx context.Context, userID int64, days int) ([]WeightPoint, error) {
	rows, err := r.pool.Query(ctx, `
		select current_weight.measured_on,
			avg(window_weight.weight_kg)::double precision
		from weights current_weight
		join weights window_weight
			on window_weight.user_id = current_weight.user_id
			and window_weight.measured_on between
				current_weight.measured_on - greatest($2 - 1, 0)
				and current_weight.measured_on
		where current_weight.user_id = $1
		group by current_weight.measured_on
		order by current_weight.measured_on`, userID, days)
	if err != nil {
		return nil, fmt.Errorf("list weight rolling average: %w", err)
	}
	defer rows.Close()

	points := make([]WeightPoint, 0)
	for rows.Next() {
		var point WeightPoint
		err = rows.Scan(&point.MeasuredOn, &point.AverageKG)
		if err != nil {
			return nil, fmt.Errorf("scan weight point: %w", err)
		}
		points = append(points, point)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate weight points: %w", err)
	}
	return points, nil
}
