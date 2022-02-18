# Space

Storing and fetching objects such as images.

Upload (PUT) an object:
```
curl --path-as-is -L -X PUT --data-binary @image.jpg http://localhost:7664/my/image.jpg
```

Fetch (GET) an object:
```
curl --path-as-is -L -X GET http://localhost:7664/my/image.jpg
```

Fetch (HEAD) metainfo of an object:
```
curl --path-as-is -L --head http://localhost:7664/my/image.jpg
```

