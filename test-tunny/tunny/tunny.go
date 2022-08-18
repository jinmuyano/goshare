// Copyright (c) 2014 Ashley Jeffs
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package tunny

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

//------------------------------------------------------------------------------

// Errors that are used throughout the Tunny API.
var (
	ErrPoolNotRunning = errors.New("the pool is not running")
	ErrJobNotFunc     = errors.New("generic worker not given a func()")
	ErrWorkerClosed   = errors.New("worker was closed")
	ErrJobTimedOut    = errors.New("job request timed out")
)

// Worker is an interface representing a Tunny working agent. It will be used to
// block a calling goroutine until ready to process a job, process that job
// synchronously, interrupt its own process call when jobs are abandoned, and
// clean up its resources when being removed from the pool.
//
// Each of these duties are implemented as a single method and can be averted
// when not needed by simply implementing an empty func.
type Worker interface {
	// Process will synchronously perform a job and return the result.
	Process(interface{}) interface{}

	// BlockUntilReady is called before each job is processed and must block the
	// calling goroutine until the Worker is ready to process the next job.
	BlockUntilReady()

	// Interrupt is called when a job is cancelled. The worker is responsible
	// for unblocking the Process implementation.
	Interrupt()

	// Terminate is called when a Worker is removed from the processing pool
	// and is responsible for cleaning up any held resources.
	Terminate()
}

//------------------------------------------------------------------------------

// closureWorker is a minimal Worker implementation that simply wraps a
// func(interface{}) interface{}
type closureWorker struct {
	processor func(interface{}) interface{}
}

func (w *closureWorker) Process(payload interface{}) interface{} {
	return w.processor(payload)
}

func (w *closureWorker) BlockUntilReady() {}
func (w *closureWorker) Interrupt()       {}
func (w *closureWorker) Terminate()       {}

//------------------------------------------------------------------------------

// callbackWorker is a minimal Worker implementation that attempts to cast
// each job into func() and either calls it if successful or returns
// ErrJobNotFunc.
type callbackWorker struct{}

func (w *callbackWorker) Process(payload interface{}) interface{} {
	f, ok := payload.(func())
	if !ok {
		return ErrJobNotFunc
	}
	f()
	return nil
}

func (w *callbackWorker) BlockUntilReady() {}
func (w *callbackWorker) Interrupt()       {}
func (w *callbackWorker) Terminate()       {}

//------------------------------------------------------------------------------

// Pool is a struct that manages a collection of workers, each with their own
// goroutine. The Pool can initialize, expand, compress and close the workers,
// as well as processing jobs with the workers synchronously.
type Pool struct {
	queuedJobs int64   //池子中的任务数,每增减一个任务,池子对应加减

	ctor    func() Worker    //worker,包含了任务
	workers []*workerWrapper   //并发队列,控制并发数
	reqChan chan workRequest   //包含2个chan,和放弃任务函数

	workerMut sync.Mutex  //锁
}

// 创建pool
// New creates a new Pool of workers that starts with n workers. You must
// provide a constructor function that creates new Worker types and when you
// change the size of the pool the constructor will be called to create each new
// Worker.
func New(n int, ctor func() Worker) *Pool {
	p := &Pool{
		ctor:    ctor,   //ctor是任务执行函数,--->为什么要在这里放函数不放函数结果❓
		reqChan: make(chan workRequest),     //创建一个空chan,通道类型是workRequest(workRequest是个对象{包含2个chan,一个放弃任务函数})
	}
	p.SetSize(n) //配置worker数量

	return p
}

// 初始化方法
// 将任务封装到worker中
// NewFunc creates a new Pool of workers where each worker will process using
// the provided func.
func NewFunc(n int, f func(interface{}) interface{}) *Pool {
	// 调用new函数(n,函数),new函数返回pool
	return New(n, func() Worker {
		return &closureWorker{	
			processor: f,
		}
	})
}


//NewCallback创建一个新的工作人员池，工作人员在其中转换作业负载
//转换为func（）并运行它，如果转换失败，则返回ErrNotFunc。
// NewCallback creates a new Pool of workers where workers cast the job payload
// into a func() and runs it, or returns ErrNotFunc if the cast failed.
func NewCallback(n int) *Pool {
	return New(n, func() Worker {
		return &callbackWorker{}
	})
}

//------------------------------------------------------------------------------
//进程将使用池来处理有效负载，并同步返回

//结果。任何Goroutine都可以安全地调用该进程，
//游泳池停止的话,将panic。
// Process will use the Pool to process a payload and synchronously return the
// result. Process can be called safely by any goroutines, but will panic if the
// Pool has been stopped.
// Process是用户调用方法个
func (p *Pool) Process(payload interface{}) interface{} {
	atomic.AddInt64(&p.queuedJobs, 1)   //任务数+1

	request, open := <-p.reqChan  //请求chan中取值,在worker.go101行w.run函数传入了workRequest chan),如果取到值open则为true,否则false
	if !open {
		panic(ErrPoolNotRunning)
	}

	request.jobChan <- payload   //payload是任务函数参数,传给jobchan

	payload, open = <-request.retChan
	if !open {
		panic(ErrWorkerClosed)
	}

	atomic.AddInt64(&p.queuedJobs, -1)  //任务队列任务数减1
	return payload
}

// ProcessTimed will use the Pool to process a payload and synchronously return
// the result. If the timeout occurs before the job has finished the worker will
// be interrupted and ErrJobTimedOut will be returned. ProcessTimed can be
// called safely by any goroutines.
func (p *Pool) ProcessTimed(
	payload interface{},
	timeout time.Duration,
) (interface{}, error) {
	atomic.AddInt64(&p.queuedJobs, 1)
	defer atomic.AddInt64(&p.queuedJobs, -1)

	tout := time.NewTimer(timeout)

	var request workRequest
	var open bool

	select {
	case request, open = <-p.reqChan:
		if !open {
			return nil, ErrPoolNotRunning
		}
	case <-tout.C:
		return nil, ErrJobTimedOut
	}

	select {
	case request.jobChan <- payload:
	case <-tout.C:
		request.interruptFunc()
		return nil, ErrJobTimedOut
	}

	select {
	case payload, open = <-request.retChan:
		if !open {
			return nil, ErrWorkerClosed
		}
	case <-tout.C:
		request.interruptFunc()
		return nil, ErrJobTimedOut
	}

	tout.Stop()
	return payload, nil
}

// ProcessCtx will use the Pool to process a payload and synchronously return
// the result. If the context cancels before the job has finished the worker will
// be interrupted and ErrJobTimedOut will be returned. ProcessCtx can be
// called safely by any goroutines.
func (p *Pool) ProcessCtx(ctx context.Context, payload interface{}) (interface{}, error) {
	atomic.AddInt64(&p.queuedJobs, 1)
	defer atomic.AddInt64(&p.queuedJobs, -1)

	var request workRequest
	var open bool

	select {
	case request, open = <-p.reqChan:
		if !open {
			return nil, ErrPoolNotRunning
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case request.jobChan <- payload:
	case <-ctx.Done():
		request.interruptFunc()
		return nil, ctx.Err()
	}

	select {
	case payload, open = <-request.retChan:
		if !open {
			return nil, ErrWorkerClosed
		}
	case <-ctx.Done():
		request.interruptFunc()
		return nil, ctx.Err()
	}

	return payload, nil
}

// QueueLength returns the current count of pending queued jobs.
func (p *Pool) QueueLength() int64 {
	return atomic.LoadInt64(&p.queuedJobs)
}

// 设置pool中worker数量
// SetSize changes the total number of workers in the Pool. This can be called
// by any goroutine at any time unless the Pool has been stopped, in which case
// a panic will occur.
// p := &Pool{
// 	ctor:    ctor,   //ctor是任务执行函数,
// 	reqChan: make(chan workRequest),     //创建一个chan,通道类型是workRequest(workRequest是个对象{包含2个chan,一个放弃任务函数})
// }
func (p *Pool) SetSize(n int) {
	p.workerMut.Lock()  //获取锁
	defer p.workerMut.Unlock()   //执行完释放锁

	lWorkers := len(p.workers)   //判断woker数量,初始化时workers是空的
	if lWorkers == n {
		return
	}

	// Add extra workers if N > len(workers)
	// workers队列中都是newWorkerWrapper函数返回的  workerWrapper
												// w := workerWrapper{
												// 	worker:        worker,
												// 	interruptChan: make(chan struct{}),
												// 	reqChan:       reqChan,//这是一个空chanel,pool初始化定义的
												// 	closeChan:     make(chan struct{}),
												// 	closedChan:    make(chan struct{}),
												// }
	for i := lWorkers; i < n; i++ {
		p.workers = append(p.workers, newWorkerWrapper(p.reqChan, p.ctor()))  // p.ctor()是&closureWorker{processor: f,},f是任务函数😈
	}

	// Asynchronously stop all workers > N
	// workerWrapper的stop函数
	// func (w *workerWrapper) stop() {
	// 	close(w.closeChan)
	// }
	for i := n; i < lWorkers; i++ {
		p.workers[i].stop()
	}

	// Synchronously wait for all workers > N to stop
	for i := n; i < lWorkers; i++ {
		p.workers[i].join()
		p.workers[i] = nil
	}

	// Remove stopped workers from slice
	p.workers = p.workers[:n]
}

// GetSize returns the current size of the pool.
func (p *Pool) GetSize() int {
	p.workerMut.Lock()
	defer p.workerMut.Unlock()

	return len(p.workers)
}

// Close will terminate all workers and close the job channel of this Pool.
func (p *Pool) Close() {
	p.SetSize(0)
	close(p.reqChan)
}

//------------------------------------------------------------------------------
