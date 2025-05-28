package investments

import (
	"context"
	// "errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"github.com/akaraon8bit/go4vercel"
	"myapp/api/events"
		 "myapp/api/cron"
		  "myapp/api/utils"
)

func RecordEvent(db *mongo.Database, eventType string, payload map[string]interface{}) error {
	event := events.Event{
		Type:      eventType,
		Payload:   payload,
		CreatedAt: time.Now(),
		Status:    "pending",
	}

	_, err := db.Collection("events").InsertOne(context.Background(), event)
	return err
}



func CreateInvestmentHandler(db *mongo.Database) gee.HandlerFunc {
    return func(ctx *gee.Context) {
        userID, err := primitive.ObjectIDFromHex(ctx.GetString("user_id"))
        if err != nil {
            ctx.JSON(http.StatusBadRequest, gee.H{"error": "Invalid user ID"})
            return
        }

        var input struct {
            PlanID string  `json:"plan_id"`
            Amount float64 `json:"amount"`
        }

        if err := ctx.ShouldBindJSON(&input); err != nil {
            ctx.JSON(http.StatusBadRequest, gee.H{"error": "Invalid request payload"})
            return
        }

        planID, err := primitive.ObjectIDFromHex(input.PlanID)
        if err != nil {
            ctx.JSON(http.StatusBadRequest, gee.H{"error": "Invalid plan ID"})
            return
        }

        // Get investment plan
        var plan struct {
            ID          primitive.ObjectID `bson:"_id"`
            Name        string             `bson:"name"`
            MinAmount   float64            `bson:"min_amount"`
            MaxAmount   float64            `bson:"max_amount"`
            DailyReturn float64            `bson:"daily_return"` // Percentage (e.g., 1.5 for 1.5%)
            TotalReturn float64            `bson:"total_return"` // Percentage
            Duration    int                `bson:"duration"`
        }

        err = db.Collection("investment_plans").FindOne(ctx.Req.Context(), bson.M{"_id": planID}).Decode(&plan)
        if err != nil {
            if err == mongo.ErrNoDocuments {
                ctx.JSON(http.StatusNotFound, gee.H{"error": "Investment plan not found"})
            } else {
                log.Printf("Error fetching investment plan: %v", err)
                ctx.JSON(http.StatusInternalServerError, gee.H{"error": "Failed to fetch investment plan"})
            }
            return
        }

        // Validate amount
        if input.Amount < plan.MinAmount || input.Amount > plan.MaxAmount {
            ctx.JSON(http.StatusBadRequest, gee.H{
                "error": fmt.Sprintf("Amount must be between %.2f and %.2f", plan.MinAmount, plan.MaxAmount),
            })
            return
        }

        // Check user balance
        var user struct {
            Wallet struct {
                Balance float64 `bson:"balance"`
            } `bson:"wallet"`
        }

        err = db.Collection("users").FindOne(ctx.Req.Context(), bson.M{"_id": userID}).Decode(&user)
        if err != nil {
            log.Printf("Error fetching user balance: %v", err)
            ctx.JSON(http.StatusInternalServerError, gee.H{"error": "Failed to verify balance"})
            return
        }

        if user.Wallet.Balance < input.Amount {
            ctx.JSON(http.StatusBadRequest, gee.H{"error": "Insufficient balance"})
            return
        }

        // Calculate investment details
        startDate := time.Now()
        endDate := startDate.Add(time.Duration(plan.Duration) * 24 * time.Hour)
        dailyROI := plan.DailyReturn // Store as percentage
        totalROI := input.Amount * (plan.TotalReturn / 100)

        // Start a session for transaction
        session, err := db.Client().StartSession()
        if err != nil {
            log.Printf("Error starting session: %v", err)
            ctx.JSON(http.StatusInternalServerError, gee.H{"error": "Failed to create investment"})
            return
        }
        defer session.EndSession(ctx.Req.Context())

        var result *mongo.InsertOneResult
        _, err = session.WithTransaction(ctx.Req.Context(), func(sessCtx mongo.SessionContext) (interface{}, error) {
            // Create investment
            investment := bson.M{
                "user_id":       userID,
                "plan_id":       planID,
                "plan_name":     plan.Name,
                "amount":        input.Amount,
                "current_value": input.Amount,
                "roi_daily":     dailyROI,
                "total_roi":     totalROI,
                "duration":      plan.Duration,
                "start_date":    startDate,
                "end_date":      endDate,
                "status":       "active",
                "progress":     0,
                "returns":      []bson.M{},
                "created_at":   startDate,
                "updated_at":   startDate,
            }

            // Insert investment
            result, err = db.Collection("investments").InsertOne(sessCtx, investment)
            if err != nil {
                return nil, err
            }

            // Deduct from user's balance and update membership
            _, err = db.Collection("users").UpdateOne(
                sessCtx,
                bson.M{"_id": userID},
                bson.M{
                    "$inc": bson.M{
                        "wallet.balance":       -input.Amount,
												"wallet.withdrawable": -input.Amount,
                        "wallet.total_invested": +input.Amount,
                    },
                    "$set": bson.M{
                        "membership": "Premium Member",
                    },
                },
            )
            if err != nil {
                return nil, err
            }

            // Create transaction record
            _, err = db.Collection("transactions").InsertOne(sessCtx, bson.M{
                "user_id":        userID,
                "type":         "Investment",
                "amount":       input.Amount,
                "description":  fmt.Sprintf("Investment in %s plan", plan.Name),
                "status":       "Completed",
                "reference":    "INV-" +  utils.TransactionFormat(utils.TransactionID(result.InsertedID.(primitive.ObjectID).Hex())),
                "investment_id": result.InsertedID,
                "created_at":   startDate,
                "updated_at":   startDate,
            })
            if err != nil {
                return nil, err
            }

            // Record investment event
            eventPayload := map[string]interface{}{
                "user_id":       userID,
                "investment_id": result.InsertedID,
                "email":        ctx.GetString("email"),
                "amount":       input.Amount,
                "plan_name":    plan.Name,
            }

            eventCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
            defer cancel()

            if err := cron.RecordAndProcessEvent(db, eventCtx,"investment_created", eventPayload); err != nil {
                log.Printf("Error recording investment event: %v", err)
            }

            return nil, nil
        })

        if err != nil {
            log.Printf("Error creating investment: %v", err)
            ctx.JSON(http.StatusInternalServerError, gee.H{"error": "Failed to create investment"})
            return
        }

        // Update user's portfolio analytics after successful investment creation
        if err := UpdateUserPortfolioAnalytics(db, userID); err != nil {
            log.Printf("Error updating portfolio analytics: %v", err)
            // Don't fail the request, just log the error
        }

        ctx.JSON(http.StatusCreated, gee.H{
            "message":      "Investment created successfully",
            "investment_id": result.InsertedID,
        })
    }
}



func UpdateUserPortfolioAnalytics(db *mongo.Database, userID primitive.ObjectID) error {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    now := time.Now().UTC()
    today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

    // First check if portfolio analytics already updated today
    var existingAnalytics struct {
        UpdatedAt time.Time `bson:"updated_at"`
    }
    err := db.Collection("portfolio_analytics").FindOne(
        ctx,
        bson.M{
            "user_id": userID,
            "date": bson.M{
                "$gte": today,
                "$lt":  today.Add(24 * time.Hour),
            },
        },
        options.FindOne().SetProjection(bson.M{"updated_at": 1}),
    ).Decode(&existingAnalytics)

    if err == nil {
        // Analytics already updated today, skip
        return nil
    }
    if err != mongo.ErrNoDocuments {
        return fmt.Errorf("error checking for existing portfolio analytics: %v", err)
    }

    // Get the user
    var user struct {
        Wallet struct {
            Balance      float64 `bson:"balance"`
            Profit       float64 `bson:"profit"`
            Withdrawable float64 `bson:"withdrawable"`
            TotalInvested float64 `bson:"total_invested"`
            TotalWithdrawn float64 `bson:"total_withdrawn"`
        } `bson:"wallet"`
    }

    err = db.Collection("users").FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
    if err != nil {
        return fmt.Errorf("error fetching user: %v", err)
    }

    // Get user's active investments
    var investments []struct {
        ROIDaily float64 `bson:"roi_daily"`
        Amount   float64 `bson:"amount"`
    }

    invCursor, err := db.Collection("investments").Find(ctx, bson.M{
        "user_id": userID,
        "status": "active",
    })
    if err != nil {
        return fmt.Errorf("error fetching investments for user %s: %v", userID.Hex(), err)
    }
    defer invCursor.Close(ctx)

    if err = invCursor.All(ctx, &investments); err != nil {
        return fmt.Errorf("error decoding investments for user %s: %v", userID.Hex(), err)
    }

    // Calculate performance metrics
    dailyEarnings := 0.0
    for _, inv := range investments {
        dailyEarnings += inv.Amount * (inv.ROIDaily / 100)
    }

    portfolioGrowth := 0.0
    if user.Wallet.TotalInvested > 0 {
        portfolioGrowth = (user.Wallet.Profit / user.Wallet.TotalInvested) * 100
    }

    // Create portfolio analytics record with upsert
    analytics := bson.M{
        "user_id":            userID,
        "date":              today,
        "total_balance":      user.Wallet.Balance,
        "total_invested":    user.Wallet.TotalInvested,
        "total_profit":      user.Wallet.Profit,
        "active_investments": len(investments),
        "daily_earnings":    dailyEarnings,
        "portfolio_growth":  portfolioGrowth,
        "created_at":        now,
        "updated_at":        now,
    }

    _, err = db.Collection("portfolio_analytics").UpdateOne(
        ctx,
        bson.M{
            "user_id": userID,
            "date": bson.M{
                "$gte": today,
                "$lt":  today.Add(24 * time.Hour),
            },
        },
        bson.M{"$set": analytics},
        options.Update().SetUpsert(true),
    )
    if err != nil {
        return fmt.Errorf("error saving portfolio analytics for user %s: %v", userID.Hex(), err)
    }

    return nil
}
