# Space

An image server that creates thumbnails on the fly 
with the specified dimensions from the original image.
The images are stored in the file system.

The thumbnails are created using imagemagick.

## Configure the server modifying the config file

Create a new config file based on the [config.toml](config.toml) template
or modify the template.

## Start the server

If the config file path is empty it defaults to `config.toml`
in the current directory.

Run the following in the project's root directory:
```
go run cmd/space/main.go -f config.toml
```

## Upload an original image

The general format of the source url is: 
`http://localhost:7664/source/` + `my/path/to/image.jpg`

```
curl -X PUT --data-binary @image.jpg http://localhost:7664/source/path/to/image.jpg
```

## Thumbnail with configured default width, aspect ration preserved

The general format of the thumbnail url is: 
`http://localhost:7664/thumbnail/` + `my/path/to/image.jpg`

```
curl -X GET http://localhost:7664/thumbnail/path/to/image.jpg
```

## Thumbnail, only width given, aspect ratio preserved
```
curl -X GET http://localhost:7664/thumbnail/path/to/image.jpg?w=600
```

## Thumbnail fits inside the given width and height dimensions, aspect ratio preserved
```
curl -X GET http://localhost:7664/thumbnail/path/to/image.jpg?w=360&h=203&m=1
```

## Thumbnail covers the given width and height dimensions, aspect ratio preserved

The image will be cut to fit it exactly:
```
curl -X GET http://localhost:7664/thumbnail/path/to/image.jpg?w=360&h=203&m=2
```

## Thumbnail covers the given width and height dimensions, aspect ratio ignored
```
curl -X GET http://localhost:7664/thumbnail/path/to/image.jpg?w=360&h=203&m=3
```

## Fetch only the headers of the thumbnail image
```
curl --head http://localhost:7664/thumbnail/path/to/image.jpg
```

## Fetch the original image
```
curl -X GET http://localhost:7664/source/path/to/image.jpg
```

## Fetch only the headers of the original image
```
curl --head http://localhost:7664/source/path/to/image.jpg
```

