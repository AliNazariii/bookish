package broker

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"therealbroker/internal/config"
	"therealbroker/internal/repositories"
	"therealbroker/pkg/broker"
	"therealbroker/pkg/cassandra"
	"therealbroker/pkg/logger"
)

var (
	service broker.Broker
	ctx     = context.Background()
	letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
)

func TestMain(m *testing.M) {
	rand.Seed(time.Now().Unix())

	logger.Configure("DEBUG")

	conf := config.New("test_broker")

	//brokerPostgres := database.NewPostgresDB(logger, &conf.Postgres, &repositories.MessagePostgres{})
	//messageRepo := repositories.NewMessagesPostgres(brokerPostgres, logger)

	brokerCassandra := cassandra.NewDB(&conf.Cassandra)
	messageRepo := repositories.NewCasMessageRepo(brokerCassandra, &conf.Cassandra)

	service = NewModule(messageRepo)
	m.Run()
}

func TestPublishShouldFailOnClosed(t *testing.T) {
	msg := createMessage()

	err := service.Close()
	assert.Nil(t, err)

	_, err = service.Publish(ctx, "ali", msg)
	assert.Equal(t, broker.ErrUnavailable, err)
}

func TestSubscribeShouldFailOnClosed(t *testing.T) {
	err := service.Close()
	assert.Nil(t, err)

	_, err = service.Subscribe(ctx, "ali")
	assert.Equal(t, broker.ErrUnavailable, err)
}

func TestFetchShouldFailOnClosed(t *testing.T) {
	err := service.Close()
	assert.Nil(t, err)

	_, err = service.Fetch(ctx, "ali", rand.Intn(100))
	assert.Equal(t, broker.ErrUnavailable, err)
}

func TestPublishShouldNotFail(t *testing.T) {
	msg := createMessage()

	_, err := service.Publish(ctx, "ali", msg)

	assert.Equal(t, nil, err)
}

func TestSubscribeShouldNotFail(t *testing.T) {
	sub, err := service.Subscribe(ctx, "ali")

	assert.Equal(t, nil, err)
	assert.NotEqual(t, nil, sub)
}

func TestPublishShouldSendMessageToSubscribedChan(t *testing.T) {
	msg := createMessage()

	sub, _ := service.Subscribe(ctx, "ali")
	_, _ = service.Publish(ctx, "ali", msg)
	in := <-sub

	assert.Equal(t, msg, in)
}

func TestPublishShouldSendMessageToSubscribedChans(t *testing.T) {
	msg := createMessage()

	sub1, _ := service.Subscribe(ctx, "ali")
	sub2, _ := service.Subscribe(ctx, "ali")
	sub3, _ := service.Subscribe(ctx, "ali")
	_, _ = service.Publish(ctx, "ali", msg)
	in1 := <-sub1
	in2 := <-sub2
	in3 := <-sub3

	assert.Equal(t, msg, in1)
	assert.Equal(t, msg, in2)
	assert.Equal(t, msg, in3)
}

func TestPublishShouldPreserveOrder(t *testing.T) {
	n := 50
	messages := make([]broker.Message, n)
	sub, _ := service.Subscribe(ctx, "ali")
	for i := 0; i < n; i++ {
		messages[i] = createMessage()
		_, _ = service.Publish(ctx, "ali", messages[i])
	}

	for i := 0; i < n; i++ {
		msg := <-sub
		assert.Equal(t, messages[i], msg)
	}
}

func TestPublishShouldNotSendToOtherSubscriptions(t *testing.T) {
	msg := createMessage()
	ali, _ := service.Subscribe(ctx, "ali")
	maryam, _ := service.Subscribe(ctx, "maryam")

	_, _ = service.Publish(ctx, "ali", msg)
	select {
	case m := <-ali:
		assert.Equal(t, msg, m)
	case <-maryam:
		assert.Fail(t, "Wrong message received")
	}
}

func TestNonExpiredMessageShouldBeFetchable(t *testing.T) {
	msg := createMessageWithExpire(time.Second * 10)
	id, _ := service.Publish(ctx, "ali", msg)
	fMsg, _ := service.Fetch(ctx, "ali", id)

	assert.Equal(t, msg, fMsg)
}

func TestExpiredMessageShouldNotBeFetchable(t *testing.T) {
	msg := createMessageWithExpire(time.Millisecond * 500)
	id, _ := service.Publish(ctx, "ali", msg)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	<-ticker.C
	fMsg, err := service.Fetch(ctx, "ali", id)
	assert.Equal(t, broker.ErrExpiredID, err)
	assert.Equal(t, broker.Message{}, fMsg)
}

func TestNewSubscriptionShouldNotGetPreviousMessages(t *testing.T) {
	msg := createMessage()
	_, _ = service.Publish(ctx, "ali", msg)
	sub, _ := service.Subscribe(ctx, "ali")

	select {
	case <-sub:
		assert.Fail(t, "Got previous message")
	default:
	}
}

func TestConcurrentSubscribesOnOneSubjectShouldNotFail(t *testing.T) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var wg sync.WaitGroup

	for {
		select {
		case <-ticker.C:
			wg.Wait()
			return

		default:
			wg.Add(1)
			go func() {
				defer wg.Done()

				_, err := service.Subscribe(ctx, "ali")
				assert.Nil(t, err)
			}()
		}
	}
}

func TestConcurrentSubscribesShouldNotFail(t *testing.T) {
	ticker := time.NewTicker(2000 * time.Millisecond)
	defer ticker.Stop()
	var wg sync.WaitGroup

	for {
		select {
		case <-ticker.C:
			wg.Wait()
			return

		default:
			wg.Add(1)
			go func() {
				defer wg.Done()

				_, err := service.Subscribe(ctx, randomString(4))
				assert.Nil(t, err)
			}()
		}
	}
}

func TestConcurrentPublishOnOneSubjectShouldNotFail(t *testing.T) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var wg sync.WaitGroup

	msg := createMessage()

	for {
		select {
		case <-ticker.C:
			wg.Wait()
			return

		default:
			wg.Add(1)
			go func() {
				defer wg.Done()

				_, err := service.Publish(ctx, "ali", msg)
				assert.Nil(t, err)
			}()
		}
	}
}

func TestConcurrentPublishShouldNotFail(t *testing.T) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var wg sync.WaitGroup

	msg := createMessage()

	for {
		select {
		case <-ticker.C:
			wg.Wait()
			return

		default:
			wg.Add(1)
			go func() {
				defer wg.Done()

				_, err := service.Publish(ctx, randomString(4), msg)
				assert.Nil(t, err)
			}()
		}
	}
}

func TestDataRace(t *testing.T) {
	duration := 500 * time.Millisecond
	ticker := time.NewTicker(duration)
	defer ticker.Stop()
	var wg sync.WaitGroup

	ids := make(chan int, 100000)

	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-ticker.C:
				return

			default:
				id, err := service.Publish(ctx, "ali", createMessageWithExpire(duration))
				ids <- id
				assert.Nil(t, err)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-ticker.C:
				return

			default:
				_, err := service.Subscribe(ctx, "ali")
				assert.Nil(t, err)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-ticker.C:
				return

			case id := <-ids:
				_, err := service.Fetch(ctx, "ali", id)
				assert.Nil(t, err)
			}
		}
	}()

	wg.Wait()
}

func BenchmarkPublish(b *testing.B) {
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := service.Publish(ctx, randomString(2), createMessage())
		assert.Nil(b, err)
	}
}

func BenchmarkSubscribe(b *testing.B) {
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := service.Subscribe(ctx, randomString(2))
		assert.Nil(b, err)
	}
}

func randomString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func createMessage() broker.Message {
	body := randomString(16)

	return broker.Message{
		Body:       body,
		Expiration: 0,
	}
}

func createMessageWithExpire(duration time.Duration) broker.Message {
	body := randomString(16)

	return broker.Message{
		Body:       body,
		Expiration: duration,
	}
}
