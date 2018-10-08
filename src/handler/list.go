package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/cndjp/qicoo-api/src/pool"
	"github.com/gomodule/redigo/redis"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// QuestionList Questionを複数格納するstruck
type QuestionList struct {
	Object string     `json:"object"`
	Type   string     `json:"type"`
	Data   []Question `json:"data"`
}

// Question Questionオブジェクトを扱うためのstruct
type Question struct {
	ID        string    `json:"id" db:"id"`
	Object    string    `json:"object" db:"object"`
	Username  string    `json:"username" db:"username"`
	EventID   string    `json:"event_id" db:"event_id"`
	ProgramID string    `json:"program_id" db:"program_id"`
	Comment   string    `json:"comment" db:"comment"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
	Like      int       `json:"like" db:"like_count"`
}

type MuxVars struct {
	EventID string
	Start   int
	End     int
	Sort    string
	Order   string
}

type PoolInterface interface {
	GetRedisConnection() (conn redis.Conn)
	selectRedisCommand() (redisCommand string)
	selectRedisSortedKey() (sortedkey string) 
	GetQuestionList() (questionList QuestionList)
	getQuestionsKey()
	checkRedisKey()
	syncQuestion(eventID string)
}


type RedisClient struct {
	Vars             MuxVars
	RedisConn        redis.Conn
//	PIface           PoolInterface
	QuestionsKey     string
	LikeSortedKey    string
	CreatedSortedKey string
}

// test
//type RedisPoolTest struct {
//}

// test
//func (rp RedisPoolTest) GetRedisConnection() redis.Conn {
//	return pool.RedisPool.Get()
//}

// GetRedisConnection
func GetInterfaceRedisConnection(p PoolInterface) (conn redis.Conn) {
	return p.GetRedisConnection()
}

func (p *RedisClient) GetRedisConnection() (conn redis.Conn) {
	return pool.RedisPool.Get()
}

// QuestionListHandler QuestionオブジェクトをRedisから取得する。存在しない場合はDBから取得し、Redisへ格納する
func QuestionListHandler(w http.ResponseWriter, r *http.Request) {
	// RedisClientの初期化初期設定
	p := new(RedisClient)

	// URLに含まれている event_id を取得
	vars := mux.Vars(r)
	start, err := strconv.Atoi(vars["start"])
	if err != nil {
		logrus.Error(err)
		return
	}
	end, err := strconv.Atoi(vars["end"])
	if err != nil {
		logrus.Error(err)
		return
	}

	p.Vars = MuxVars{
		EventID: vars["event_id"],
		Start:   start,
		End:     end,
		Sort:    vars["sort"],
		Order:   vars["order"],
	}

	//m := new(RedisPoolTest)
	//p.PIface = m
	p.RedisConn = GetInterfaceRedisConnection(p)
	defer p.RedisConn.Close()

	// 多分並列処理できるやつ
	/* Redisにデータが存在するか確認する。 */
	p.checkRedisKey()

	questionList := p.GetQuestionList()

	/* JSONの整形 */
	// QuestionのStructをjsonとして変換
	jsonBytes, err := json.Marshal(questionList)
	if err != nil {
		logrus.Error(err)
		return
	}

	// 整形用のバッファを作成し、整形を実行
	out := new(bytes.Buffer)
	// プリフィックスなし、スペース2つでインデント
	json.Indent(out, jsonBytes, "", "  ")

	w.Write([]byte(out.String()))

}

func (p *RedisClient) selectRedisCommand() (redisCommand string) {
	switch p.Vars.Order {
	case "asc":
		return "ZRANGE"
	case "desc":
		return "ZREVRANGE"
	default:
		return "ZRANGE"
	}
}

func (p *RedisClient) selectRedisSortedKey() (sortedkey string) {
	switch p.Vars.Sort {
	case "created_at":
		return p.CreatedSortedKey
	case "like":
		return p.LikeSortedKey
	default:
		return p.LikeSortedKey
	}
}

// GetQuestionList RedisとDBからデータを取得する
func (p *RedisClient) GetQuestionList() (questionList QuestionList) {
	p.getQuestionsKey()

	// API実行時に指定されたSortをRedisで実行
	uuidSlice, err := redis.Strings(p.RedisConn.Do(p.selectRedisCommand(), p.selectRedisSortedKey(), p.Vars.Start-1, p.Vars.End-1))
	if err != nil {
		logrus.Error(err)
		return
	}

	// RedisのDo関数は、Interface型のSliceしか受け付けないため、makeで生成 (String型のSliceはコンパイルエラー)
	// Example) HMGET questions_jks1812 questionID questionID questionID questionID ...
	var list = make([]interface{}, 0, 20)
	list = append(list, p.QuestionsKey)
	for _, str := range uuidSlice {
		list = append(list, str)
	}

	bytesSlice, err := redis.ByteSlices(p.RedisConn.Do("HMGET", list...))
	if err != nil {
		logrus.Error(err)
		return
	}

	var questions []Question
	for _, bytes := range bytesSlice {
		q := new(Question)
		json.Unmarshal(bytes, q)
		questions = append(questions, *q)
	}

	// DB or Redis から取得したデータのtimezoneをAsia/Tokyoと指定
	locationTokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		logrus.Fatal(err)
	}

	for i := range questions {
		questions[i].CreatedAt = questions[i].CreatedAt.In(locationTokyo)
		questions[i].UpdatedAt = questions[i].UpdatedAt.In(locationTokyo)
	}

	questionList = QuestionList{
		Data:   questions,
		Object: "list",
		Type:   "question",
	}

	return questionList
}

func (p *RedisClient) getQuestionsKey() {
	p.QuestionsKey = "questions_" + p.Vars.EventID
	p.LikeSortedKey = p.QuestionsKey + "_like"
	p.CreatedSortedKey = p.QuestionsKey + "_created"

	return
}

// redisHasKey
func redisHasKey(conn redis.Conn, key string) (hasKey bool) {
	hasInt, err := redis.Int(conn.Do("EXISTS", key))
	if err != nil {
		logrus.Error(err)
		return false
	}

	switch hasInt {
	case 1:
		hasKey = true
	default:
		hasKey = false
	}

	return
}
