package pipeline

import "time"

// Option 是 Pipeline 的可选配置。
type Option func(*pipeline)

// WithRetry 配置 pipeline 的双层 retry 上限与间隔。
//
//	maxChannelRetries      跨 channel 切换次数上限（不含首次 attempt）。
//	maxSameChannelRetries  单 channel 内重试次数上限。
//	retryDelay             相邻 attempt 之间的等待时长；0 表示无等待。
func WithRetry(maxChannelRetries, maxSameChannelRetries int, retryDelay time.Duration) Option {
	return func(p *pipeline) {
		p.maxChannelRetries = maxChannelRetries
		p.maxSameChannelRetries = maxSameChannelRetries
		p.retryDelay = retryDelay
	}
}

// WithMiddlewares 追加一组 middleware；多次调用按顺序累加。
//
// Middleware 的 Raw* / Llm* hook 按注册顺序执行（先注册先执行）；
// Error hook 同样按注册顺序调用，不短路。
func WithMiddlewares(mws ...Middleware) Option {
	return func(p *pipeline) {
		p.middlewares = append(p.middlewares, mws...)
	}
}

// WithEmptyResponseDetection 启用空流检测。
//
// 启用后 pipeline 会在把 llm.Stream 交给 Inbound 之前预读若干 event，
// 若流正常结束但未产生有效内容则返回 ErrEmptyResponse，由 retry 分支处理。
func WithEmptyResponseDetection() Option {
	return func(p *pipeline) {
		p.emptyResponseDetection = true
	}
}
