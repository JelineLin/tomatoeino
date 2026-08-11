# English Coach Desktop

这是 English Coach Web App 的 Wails 桌面壳。页面被打包进 `.app`，API 请求仍发送到
同一个 English Coach 后端，因此 Web 和桌面共享访问码、课程、学习进度和周报。

桌面应用不包含方舟/OpenAI 密钥，也不在本机创建另一份学习数据库。默认通过
`https://jelinelin.com/api/english/*` 访问独立 English Coach 进程；本地联调时可覆盖：

```bash
ENGLISH_DESKTOP_API_URL=http://127.0.0.1:8450 make desktop-build
```

构建过程会先使用 npm 静态导出 `english-web`，再由 Wails 把结果嵌入 macOS `.app`。
最终应用运行不需要 Node.js。产物位于 `desktop/build/bin/EnglishCoach.app`。

Wails 打包会执行框架自己的资源/绑定生成步骤，按仓库约定由开发者手动运行：

```bash
make desktop-test
make desktop-build
```
