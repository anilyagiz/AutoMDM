[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/anilyagiz/automdm)

# AutoMDM deployment guide

This guide outlines the services configured in the Render blueprint for deploying the AutoMDM system, which includes a web service, a MySQL database, and a Redis server.

## Services overview

### 1. AutoMDM web service
- **Type:** Web
- **Runtime:** Image
- **Image:** `automdm/automdm:latest` 
- **Description:** Main web service running the AutoMDM application, which is deployed using the latest AutoMDM Docker image. Configured to prepare the database before deployment.
- **Health check path:** `/healthz`
- **Environment variables:** Connects to MySQL and Redis using service-bound environment variables.

### 2. AutoMDM MySQL database
- **Type:** Private service (pserv)
- **Runtime:** Image
- **Image:** [`docker.io/library/mysql:8.0.44`](https://hub.docker.com/_/mysql)
- **Disk:** 10 GB mounted at `/var/lib/mysql`
- **Description:** MySQL database used by the AutoMDM web service. Environment variables for database credentials are managed within the service and some are automatically generated.

### 3. AutoMDM Redis service
- **Type:** Private service (pserv)
- **Runtime:** Image
- **Repository:** [Redis Docker image](https://hub.docker.com/_/redis)
- **Description:** Redis service for caching and other in-memory data storage needs of the AutoMDM web service.

## Deployment guide

### Prerequisites
- You need an account on [Render](https://render.com).
- Familiarity with Render's dashboard and deployment concepts.

### Steps to deploy

Click the deploy on render button or import the blueprint from the Render service deployment dashboard.

### Post-deployment

Navigate to the generated URL and run through the initial setup. If you have a license key you can add it post-deploy as
an environment variable `AUTOMDM_LICENSE_KEY=value` in the AutoMDM service configuration.
