# WeKnora 个人知识库

这是基于 WeKnora v0.8.0 发布提交 `1edcd54b` 的独立个人版源码。保留原有知识库、搜索问答、自动 Wiki、文件和网页导入等能力；个人界面隐藏团队和不使用的入口。新增 `tencent_docs` 数据源连接器，复用原有同步调度、日志和删除开关。

## 构建与运行

- 前端在 `frontend/` 中以 `VITE_PERSONAL_MODE=true` 执行现有构建命令，产物 `frontend/dist/` 用 `personal/Dockerfile.personal-ui` 打包为 `weknora-personal-ui:v0.8.0`。
- 后端从 `./cmd/server` 编译为根目录的 `WeKnora-personal`，使用 `personal/Dockerfile.personal-app` 打包为 `weknora-personal-app:v0.8.0`。Windows 主机建议使用 `personal/Dockerfile.go-builder` 提供的 Linux 构建环境。
- 以原项目 `docker-compose.yml` 加载 `personal/compose.personal.yml`，只启动 `postgres`、`redis`、`docreader`、`app`、`frontend` 五个服务。具体端口、模型和密钥通过本地 `.env` 配置；本仓库只保留 `.env.example`。
- `personal/backup-weknora.ps1` 是 Windows Docker 部署的备份脚本，备份保存在本地 `data/backups/`，不会纳入 Git。

当前代码已在隔离测试栈完成腾讯文档企业版只读连接、入库、检索和重复同步验证。个人版授权接口及 PDF、图片内容读取仍需有相应权限的真实账号验证；只会同步接口能稳定取得的内容。

本仓库不含个人资料、数据库、文件卷、备份、账号凭证、访问令牌和构建产物。上游项目许可证见 [LICENSE](LICENSE)。
