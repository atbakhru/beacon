package beacon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

func (s *Store) Publish(ctx context.Context, brokers []string) {
	writer := &kafka.Writer{Addr: kafka.TCP(brokers...), Balancer: &kafka.Hash{}, RequiredAcks: kafka.RequireAll, BatchTimeout: 5 * time.Millisecond, WriteTimeout: 10 * time.Second}
	defer writer.Close()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.publishBatch(ctx, writer); err != nil && ctx.Err() == nil {
				slog.Warn("outbox retry", "error", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
		}
	}
}
func (s *Store) publishBatch(ctx context.Context, w *kafka.Writer) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,topic,key,payload FROM outbox_events WHERE published_at IS NULL ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	type entry struct {
		id         int64
		topic, key string
		payload    []byte
	}
	items := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.id, &e.topic, &e.key, &e.payload); err != nil {
			rows.Close()
			return err
		}
		items = append(items, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	messages := make([]kafka.Message, 0, len(items))
	ids := make([]int64, 0, len(items))
	for _, e := range items {
		messages = append(messages, kafka.Message{Topic: e.topic, Key: []byte(e.key), Value: e.payload})
		ids = append(ids, e.id)
	}
	if len(messages) > 0 {
		if err = w.WriteMessages(ctx, messages...); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE outbox_events SET published_at=now() WHERE id=ANY($1)`, ids); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) Consume(ctx context.Context, brokers []string, rdb *redis.Client) {
	for _, topic := range []string{"alerts.raw.v1", "incidents.events.v1"} {
		go func(topic string) {
			reader := kafka.NewReader(kafka.ReaderConfig{Brokers: brokers, Topic: topic, GroupID: "beacon-go-" + topic, MaxBytes: 10e6, CommitInterval: 0})
			defer reader.Close()
			for {
				m, err := reader.FetchMessage(ctx)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					slog.Warn("consumer read", "error", err)
					time.Sleep(time.Second)
					continue
				}
				var e struct {
					Org   string `json:"org_id"`
					Alert string `json:"alert_id"`
				}
				err = json.Unmarshal(m.Value, &e)
				if err != nil {
					slog.Error("invalid internal event", "topic", topic)
					_ = reader.CommitMessages(ctx, m)
					continue
				}
				for ctx.Err() == nil {
					if topic == "alerts.raw.v1" {
						err = s.Correlate(ctx, e.Org, e.Alert)
					} else {
						err = rdb.Publish(ctx, "beacon:"+e.Org, string(m.Value)).Err()
					}
					if err == nil || err == pgx.ErrNoRows {
						break
					}
					slog.Warn("consumer retry", "topic", topic, "error", err)
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Second):
					}
				}
				if err = reader.CommitMessages(ctx, m); err != nil && ctx.Err() == nil {
					slog.Warn("commit retry safe", "error", err)
				}
			}
		}(topic)
	}
	<-ctx.Done()
}
func EnsureTopics(ctx context.Context, brokers []string) error {
	if len(brokers) == 0 {
		return fmt.Errorf("missing broker")
	}
	conn, err := kafka.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return err
	}
	defer conn.Close()
	controller, err := conn.Controller()
	if err != nil {
		return err
	}
	leader, err := kafka.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", controller.Host, controller.Port))
	if err != nil {
		return err
	}
	defer leader.Close()
	replication := 1
	if n, err := strconv.Atoi(os.Getenv("KAFKA_REPLICATION_FACTOR")); err == nil && n > 0 {
		replication = n
	}
	return leader.CreateTopics(kafka.TopicConfig{Topic: "alerts.raw.v1", NumPartitions: 3, ReplicationFactor: replication}, kafka.TopicConfig{Topic: "incidents.events.v1", NumPartitions: 3, ReplicationFactor: replication})
}
