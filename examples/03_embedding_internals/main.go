// 例子 03：扒开 embedding 的内部——向量到底长什么样，相似度到底怎么算
//
// 这是 embedding 学习线的「原理深挖」分支（承接 L1 假 embedder、L2 真实 embedder）。
// 前面我们已经会「用」向量检索了，这一课不堆功能，只把 cosine / 归一化 / 距离度量
// 这几件底层的事摊开在眼前手算一遍——就像你想搞懂清算逻辑时，会先在 Excel 里手敲
// 一遍对账，而不是一上来就信清算中心吐出来的数。
//
// 要亲眼验证的 4 件事：
//  1. 一个向量有两个属性：模长 |v|（多长）和 方向（指哪）。语义检索只关心「方向」。
//  2. 长度不影响 cosine：把向量放大 3 倍，cosine 纹丝不动，欧氏距离却被拉开了。
//  3. cosine 与欧氏距离的关系：对单位向量，|â-ĉ|² = 2 − 2·cosine，两者排序完全等价。
//  4. OpenAI 的 embedding 其实已经归一化（模≈1），所以 cosine 退化成点积——
//     vectorstore.Store 里「除以模长」那一步，对 OpenAI 向量是冗余的（但保留更稳妥）。
//
// 运行：
//
//	go run ./examples/03_embedding_internals                        # 只跑 Part A：纯数学，离线、免费
//	OPENAI_API_KEY=sk-xxx go run ./examples/03_embedding_internals  # 再加 Part B：拉真实向量印证
package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"

	"tomatoeino/internal/llm"
)

func main() {
	fmt.Println("======== Part A：纯数学（离线，亲手算一遍）========")
	partA()

	fmt.Println("\n======== Part B：真实 OpenAI 向量（需 OPENAI_API_KEY）========")
	partB()
}

// partA 用几个二维小向量演示「方向 vs 长度」，全部能心算验证。
func partA() {
	// 故意构造：b 是 a 的「放大版」（方向完全相同，长度是 3 倍），c 与 a 等长但不同向。
	a := []float64{3, 4} // 模 = 5（3-4-5 直角三角形）
	b := []float64{9, 12} // = 3a：方向和 a 一模一样，只是更长
	c := []float64{4, 3} // 模 = 5：和 a 等长，但指向不同

	fmt.Printf("a=%v  |a|=%.4f\n", a, norm(a))
	fmt.Printf("b=%v |b|=%.4f  (b = 3a，方向和 a 相同，只是更长)\n", b, norm(b))
	fmt.Printf("c=%v  |c|=%.4f\n", c, norm(c))

	// 关键 1：长度不影响 cosine。
	// a 和它的放大版 b，方向没变，cosine = 1（完全同向）；欧氏距离却被长度拉开。
	fmt.Printf("\ncosine(a,b)=%.4f  <- 放大 3 倍，方向没变，cosine 仍是 1\n", cosine(a, b))
	fmt.Printf("euclid(a,b)=%.4f  <- 但欧氏距离被「长度差」放大了\n", euclid(a, b))
	fmt.Println("=> 语义检索只想知道「意思像不像」(方向)，不想被「文本长短」(长度) 干扰，所以选 cosine。")
	fmt.Println("   类比：比两个投资组合「配置比例像不像」，而不是比「谁本金大」。")

	// 关键 2：对单位向量，cosine 和欧氏距离是同一件事的两种说法。
	au, cu := unit(a), unit(c)
	d2 := euclid(au, cu) * euclid(au, cu)
	fmt.Printf("\n把 a、c 归一化成单位向量 â、ĉ（模都变成 1）后：\n")
	fmt.Printf("  cosine(â,ĉ)   = %.4f\n", cosine(au, cu))
	fmt.Printf("  |â-ĉ|²        = %.4f\n", d2)
	fmt.Printf("  2 - 2·cosine  = %.4f   <- 和上面恒等\n", 2-2*cosine(au, cu))
	fmt.Println("=> 公式 |â-ĉ|² = 2 − 2·cosine。所以单位向量上：cosine 越大 <=> 欧氏越小，")
	fmt.Println("   两种度量给出的「谁更像」排序完全一致，挑哪个都行。")
}

// partB 拉几条真实 OpenAI 向量，印证 Part A 的结论，并看清真实向量的「样子」。
func partB() {
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("(未设置 OPENAI_API_KEY，跳过。配上 key 再跑，能看到真实向量的维度和模长。)")
		return
	}

	ctx := context.Background()
	emb, err := llm.NewEmbedder(ctx)
	if err != nil {
		log.Fatal(err)
	}

	texts := []string{
		"蒸排骨配玉米土豆", // 0：荤菜
		"清蒸鳕鱼挤柠檬",  // 1：荤菜（鱼）
		"白水西兰花加薄盐", // 2：素菜
	}
	vecs, err := emb.EmbedStrings(ctx, texts)
	if err != nil {
		log.Fatal(err)
	}

	// 1. 维度：text-embedding-3-small 默认把每段文字压成 1536 个数。
	fmt.Printf("向量维度 = %d（每段文字，无论多长，都被压成这么多个 float）\n", len(vecs[0]))
	fmt.Printf("第 0 个向量的前 5 维：%.4f ...\n\n", vecs[0][:5])

	// 2. 发现：OpenAI 的向量自带归一化，模长都≈1。
	for i, v := range vecs {
		fmt.Printf("|v%d| = %.6f   %s\n", i, norm(v), texts[i])
	}
	fmt.Println("=> 模长全都≈1：OpenAI 返回的本来就是「单位向量」。")
	fmt.Println("   既然已归一化，cosine = 点积(dot)，Store 里再除一次模长是冗余计算（保留无害）。")

	// 3. 印证：cosine == dot（因已归一化），且语义近的菜 cosine 更大。
	fmt.Printf("\ncosine(v0,v1)=%.4f   dot(v0,v1)=%.4f   <- 两者相等，正因为已归一化\n",
		cosine(vecs[0], vecs[1]), dot(vecs[0], vecs[1]))
	fmt.Printf("蒸排骨 vs 清蒸鳕鱼  cosine=%.4f  (都是荤菜，较像)\n", cosine(vecs[0], vecs[1]))
	fmt.Printf("蒸排骨 vs 白水西兰花 cosine=%.4f  (荤 vs 素，较远)\n", cosine(vecs[0], vecs[2]))
	fmt.Println("=> 字面几乎不重合，cosine 仍能把「荤菜更像荤菜」排出来——这就是 L2 看到的语义检索。")
}

// ---------- 下面是几个一看就懂的向量小工具，故意全手写，方便你对着公式核对 ----------

// norm 计算向量的模长 |v| = √(Σ vᵢ²)，即它「有多长」。
func norm(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x * x
	}
	return math.Sqrt(s)
}

// dot 计算点积 a·b = Σ aᵢbᵢ。它同时含「方向是否一致」和「两者长度」两重信息。
func dot(a, b []float64) float64 {
	var s float64
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// unit 把向量归一化成单位向量（长度变 1、方向不变）：v / |v|。
func unit(v []float64) []float64 {
	n := norm(v)
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x / n
	}
	return out
}

// cosine 余弦相似度 = dot(a,b) / (|a||b|)：把长度除掉，只剩「方向像不像」，范围 [-1,1]。
func cosine(a, b []float64) float64 {
	na, nb := norm(a), norm(b)
	if na == 0 || nb == 0 {
		return 0
	}
	return dot(a, b) / (na * nb)
}

// euclid 欧氏距离 = |a-b| = √(Σ(aᵢ-bᵢ)²)：两点间的直线距离，长度差会算进去。
func euclid(a, b []float64) float64 {
	var s float64
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return math.Sqrt(s)
}
