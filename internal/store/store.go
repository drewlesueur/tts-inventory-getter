package store

import (
	"context"
	"errors"

	"github.com/example/inventory-scraper/internal/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type RunStore interface {
	UpsertRun(ctx context.Context, run model.RunSummary) error
	GetRun(ctx context.Context, runID string) (model.RunSummary, error)
	FindByIdempotency(ctx context.Context, key string) (model.RunSummary, error)
}

type MongoRunStore struct { col *mongo.Collection }

func NewMongoRunStore(client *mongo.Client, dbName, collection string) *MongoRunStore {
	return &MongoRunStore{col: client.Database(dbName).Collection(collection)}
}

func (m *MongoRunStore) UpsertRun(ctx context.Context, run model.RunSummary) error {
	_, err := m.col.UpdateOne(ctx, bson.M{"runId": run.RunID}, bson.M{"$set": run}, options.Update().SetUpsert(true))
	return err
}

func (m *MongoRunStore) GetRun(ctx context.Context, runID string) (model.RunSummary, error) {
	var out model.RunSummary
	err := m.col.FindOne(ctx, bson.M{"runId": runID}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) { return model.RunSummary{}, ErrNotFound }
	return out, err
}

func (m *MongoRunStore) FindByIdempotency(ctx context.Context, key string) (model.RunSummary, error) {
	if key == "" { return model.RunSummary{}, ErrNotFound }
	var out model.RunSummary
	err := m.col.FindOne(ctx, bson.M{"idempotencyKey": key}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) { return model.RunSummary{}, ErrNotFound }
	return out, err
}

var ErrNotFound = errors.New("not found")
