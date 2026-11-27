# Kubequery and AutoMDM

Use the provided configuration file ([kubequery-automdm.yml](kubequery-automdm.yml)) to get a [kubequery](../../infrastructure/kubequery) instance connected to AutoMDM.

Before deploying, first retrieve the enroll secret from AutoMDM by opening a web browser to the AutoMDM URL, going to the Hosts page, and clicking on the "Manage enroll secret" button.
Alternatively, you can get the enroll secret using `automdmctl` using `automdmctl get enroll-secret`.
Update the `enroll.secret` in the `ConfigMap`. In production, you will also need to update the `tls_hostname` and `automdm.pem` to the appropriate values. In order to download the `automdm.pem` certificate chain, navigate to the "Hosts> Add hosts> Advanced" tab and select "Download". Finally, deploy kubequery using `kubectl`


```sh
kubectl apply -f kubequery-automdm.yml
```

Kubernetes clusters will show up in AutoMDM with hostnames like `kubequery <CLUSTER NAME>`.

Sample reports (formerly "queries") are included in the configuration file ([queries-kubequery-automdm.yml](queries-kubequery-automdm.yml)). Modify the `team` value in this file to reflect the appropriate team (formerly "team") name for your environment and apply with `automdmctl`.

```
automdmctl apply -f queries-kubequery-automdm.yml
```
