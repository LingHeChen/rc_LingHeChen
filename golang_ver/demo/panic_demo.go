// panic_demo.go — 演示 Go worker goroutine 在 panic 时的行为
//
// 运行方式：
//   go run ./demo/panic_demo.go
//
// 这个 demo 展示了三种场景：
//   1. 没有 recover 的 goroutine panic → 整个进程崩溃
//   2. 有 recover 的 goroutine panic → 只影响当前任务，其他继续运行
//   3. 当前 worker.go 的实际状态（无 recover）

package main

import (
	"fmt"
	"sync"
	"time"
)

// ─── 场景 1：复现当前 worker.go 的实际行为 ─────────────────────────────────────
//
// worker.go 中的 runWorker 没有 defer/recover：
//
//   func (w *Worker) runWorker(ctx context.Context) {
//       for {
//           job, _ := w.queue.Dequeue(ctx)
//           w.process(ctx, job)   // ← panic 在此传播，无拦截
//       }
//   }
//
// 如果 process() 或 deliver() 产生 panic（比如空指针解引用），
// 该 goroutine 立即终止，并向 runtime 报告未恢复的 panic。
// Go runtime 的行为：打印 stack trace，然后 os.Exit(2)。
// 结果：所有 5 个 worker goroutine + HTTP server 全部崩溃。

func simulateCurrentWorker() {
	var wg sync.WaitGroup
	results := make([]string, 5)

	for id := range 5 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// 模拟处理 job：job #2 触发 panic（例如空指针解引用）
			if id == 2 {
				// 没有 recover：panic 会向上传播，导致整个程序崩溃
				// 下面这行如果取消注释，整个进程会立即终止，1、3、4、5 的结果永远不会打印
				// panic(fmt.Sprintf("goroutine %d: nil pointer dereference in deliver()", id))
				results[id] = fmt.Sprintf("goroutine %d: WOULD CRASH THE ENTIRE PROCESS", id)
			} else {
				time.Sleep(10 * time.Millisecond)
				results[id] = fmt.Sprintf("goroutine %d: ok", id)
			}
		}(id)
	}

	wg.Wait()
	for _, r := range results {
		fmt.Println(" ", r)
	}
}

// ─── 场景 2：加了 recover 之后的行为 ──────────────────────────────────────────
//
// 这是"修复版"：在每个 job 处理入口加 defer/recover，
// 使 panic 只终止当前 goroutine 的当次处理，其他 goroutine 不受影响。

func workerWithRecover(id int, results []string, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() {
		if r := recover(); r != nil {
			results[id] = fmt.Sprintf("goroutine %d: PANIC recovered → job marked failed, goroutine survives", id)
		}
	}()

	if id == 2 {
		panic(fmt.Sprintf("goroutine %d: intentional panic", id))
	}

	time.Sleep(10 * time.Millisecond)
	results[id] = fmt.Sprintf("goroutine %d: ok", id)
}

func simulateFixedWorker() {
	var wg sync.WaitGroup
	results := make([]string, 5)

	for id := range 5 {
		wg.Add(1)
		go workerWithRecover(id, results, &wg)
	}

	wg.Wait()
	for _, r := range results {
		fmt.Println(" ", r)
	}
}

func main() {
	fmt.Println("=== 场景 1：当前 worker.go（无 recover）===")
	fmt.Println("goroutine #2 的 panic 已被注释掉以防止程序真的崩溃。")
	fmt.Println("实际上若取消注释，输出会在 panic 处截断，Go runtime 打印 stack trace 后 exit(2)。")
	simulateCurrentWorker()

	fmt.Println()
	fmt.Println("=== 场景 2：加了 recover 之后 ===")
	fmt.Println("goroutine #2 panic，但其他 goroutine 继续运行：")
	simulateFixedWorker()

	fmt.Println()
	fmt.Println("=== 关键区别 ===")
	fmt.Print(`
Go（当前实现，无 recover）：
  panic → goroutine 终止 → Go runtime: os.Exit(2) → 整个进程崩溃
  影响范围：所有 5 个 worker goroutine + HTTP server

Go（修复后，有 recover）：
  panic → recover() 捕获 → 当前任务失败，goroutine 继续运行
  影响范围：只有当前处理中的 job
  但注意：goroutine 本身不会重启，需要手动确保 pool size 不变

Elixir/OTP（当前实现）：
  raise → Erlang 进程终止 → Oban supervisor 收到 EXIT 信号
  → job 状态更新为 retryable → supervisor 自动重启 worker 进程
  影响范围：只有当前 job 的进程，其他 job 进程完全不知道发生了什么
  无需任何手动 recover 代码
`)
}
