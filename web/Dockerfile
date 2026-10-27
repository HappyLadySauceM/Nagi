# 运行态镜像：dist 由 CI 在 runner 内构建，这里只打包。
FROM nginx:alpine
COPY dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 8080
