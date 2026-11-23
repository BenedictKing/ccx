package metrics

import (
	"encoding/json"
	"time"
)

// 恢复全部持久化在途记录，与内存窗口和容量分离；逐批关闭读取游标后再写入。
func (s *RoutingStore) recoverInterruptedRequests() error {
	const batchSize = 256
	afterID := ""
	for {
		rows, err := s.db.Query(`SELECT request_id, payload FROM routing_records_v1
			WHERE request_id > ? AND json_extract(payload, '$.status') = ?
			ORDER BY request_id LIMIT ?`, afterID, RoutingStatusInProgress, batchSize)
		if err != nil {
			return err
		}
		requests := make([]RoutingRequest, 0, batchSize)
		for rows.Next() {
			var id, payload string
			if err := rows.Scan(&id, &payload); err != nil {
				rows.Close()
				return err
			}
			var request RoutingRequest
			if err := json.Unmarshal([]byte(payload), &request); err != nil {
				rows.Close()
				return err
			}
			requests = append(requests, request)
			afterID = id
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(requests) == 0 {
			return nil
		}
		now := time.Now()
		for _, request := range requests {
			request.Status, request.CompletedAt = RoutingStatusUnknown, &now
			for i := range request.Attempts {
				if request.Attempts[i].Status == RoutingStatusInProgress {
					request.Attempts[i].Status, request.Attempts[i].CompletedAt = RoutingStatusUnknown, &now
				}
			}
			if err := s.persist(request); err != nil {
				return err
			}
		}
	}
}
