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

//------------------------------------------------------------------------------

// workRequest is a struct containing context representing a workers intention
// to receive a work payload.
// worker创建请求之后丢到reqChan,请求池子调用
type workRequest struct {
	// jobChan is used to send the payload to this worker.
	//将任务发送给当前所在worker(workRequest包含在work中)
	jobChan chan<- interface{}

	// retChan is used to read the result from this worker.  
	// 将worker执行结果从这个chan取出
	retChan <-chan interface{}

	// interruptFunc can be called to cancel a running job. When called it is no
	// longer necessary to read from retChan.
	// 放弃任务
	interruptFunc func()
}

//------------------------------------------------------------------------------
//WorkerRapper获取一个Worker实现并将其包装在goroutine中
//以及通道布置。工作人员负责管理工作
//工人和goroutine的一生。
// workerWrapper takes a Worker implementation and wraps it within a goroutine
// and channel arrangement. The workerWrapper is responsible for managing the
// lifetime of both the Worker and the goroutine.
type workerWrapper struct {
	worker        Worker
	interruptChan chan struct{}

	// reqChan is NOT owned by this type, it is used to send requests for work.
	reqChan chan<- workRequest

	// closeChan can be closed in order to cleanly shutdown this worker.
	closeChan chan struct{}

	// closedChan is closed by the run() goroutine when it exits.
	closedChan chan struct{}
}

// 构造函数,创建workerWrapper
func newWorkerWrapper(
	reqChan chan<- workRequest,
	worker Worker, //closureWorker{}
) *workerWrapper {
	// wrap后的多了几个chan
	w := workerWrapper{
		worker:        worker,   //worker是p.ctor(),是&closureWorker{processor: f,},f是任务函数😈
		interruptChan: make(chan struct{}),
		reqChan:       reqChan,//这是一个空chanel,pool初始化定义的
		closeChan:     make(chan struct{}),
		closedChan:    make(chan struct{}),
	}

	go w.run()

	return &w
}

//------------------------------------------------------------------------------

func (w *workerWrapper) interrupt() {
	close(w.interruptChan)
	w.worker.Interrupt()   //接口,没有实现
}

func (w *workerWrapper) run() {
	jobChan, retChan := make(chan interface{}), make(chan interface{})
	defer func() {
		w.worker.Terminate()//这应该要开发自己实现再调用,没有实现
		close(retChan)
		close(w.closedChan)
	}()

	for {
		// NOTE: Blocking here will prevent the worker from closing down.
		w.worker.BlockUntilReady()  //接口拓展方法,没有实现
		select {
			// 往reqchan中传递workRequest实例,等待process函数接收调用
		case w.reqChan <- workRequest{
			jobChan:       jobChan,
			retChan:       retChan,
			interruptFunc: w.interrupt, //关闭interruptChan
		}:
			select {
				// 程序启动时,w.run启动的goruntime会阻塞在这里,等待jobchan(无缓冲的chan都会阻塞)
				// jobchan中取任务函数参数,tunny.go 171行传递
			case payload := <-jobChan:
				// closureWorker的Process方法实现
				// func (w *closureWorker) Process(payload interface{}) interface{} {
				// 	return w.processor(payload)  在这里调用用户定义的func,payload是参数
				// }
				result := w.worker.Process(payload)  
				select {
				case retChan <- result:   //将结果丢给resultchan,tunny.go 173行解除阻塞
				case <-w.interruptChan:
					w.interruptChan = make(chan struct{})
				}
			case <-w.interruptChan:
				w.interruptChan = make(chan struct{})
			}
		case <-w.closeChan:
			return //携程结束
		}
	}
}

//------------------------------------------------------------------------------

func (w *workerWrapper) stop() {
	close(w.closeChan)
}

func (w *workerWrapper) join() {
	<-w.closedChan
}

//------------------------------------------------------------------------------
